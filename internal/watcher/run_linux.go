//go:build linux

package watcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"goeverything/internal/db"
	"goeverything/internal/scanner"
)

func (w *Watcher) Run(ctx context.Context, root string) error {
	absRoot, err := validateLinuxWatchRoot(w.store, root)
	if err != nil {
		return err
	}

	notifier, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create inotify watcher: %w", err)
	}
	defer func() { _ = notifier.Close() }()

	watched := make(map[string]struct{})
	if err := addWatchTree(notifier, watched, absRoot); err != nil {
		return err
	}
	return w.runLinuxEvents(ctx, notifier, watched, absRoot)
}

func validateLinuxWatchRoot(store *db.Store, root string) (string, error) {
	if store == nil {
		return "", errors.New("watcher store is required")
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("watch root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return "", fmt.Errorf("open watch root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("watch root %q is not a directory", absRoot)
	}
	return absRoot, nil
}

func (w *Watcher) runLinuxEvents(ctx context.Context, notifier *fsnotify.Watcher, watched map[string]struct{}, root string) error {
	pending := make(map[string]fsnotify.Op)
	var debounceTimer *time.Timer
	var debounceC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-notifier.Errors:
			if !ok {
				return errors.New("inotify error channel closed")
			}
			return linuxNotifierError(err, root)
		case event, ok := <-notifier.Events:
			if !ok {
				return errors.New("inotify event channel closed")
			}
			path := filepath.Clean(event.Name)
			pending[path] |= event.Op
			resetDebounceTimer(&debounceTimer, &debounceC)
		case <-debounceC:
			if err := w.flushLinuxEvents(ctx, notifier, watched, root, pending); err != nil {
				return err
			}
			debounceC = nil
		}
	}
}

func linuxNotifierError(err error, root string) error {
	if errors.Is(err, fsnotify.ErrEventOverflow) {
		return fmt.Errorf("inotify events were lost: %w\nhint: run ge scan --root %q to reconcile the index", err, root)
	}
	if isWatchLimitError(err) {
		return fmt.Errorf("inotify watch limit reached: %w\nhint: increase fs.inotify.max_user_watches or scan a smaller root", err)
	}
	return fmt.Errorf("inotify watcher error: %w", err)
}

func resetDebounceTimer(timer **time.Timer, channel *<-chan time.Time) {
	if *timer == nil {
		*timer = time.NewTimer(50 * time.Millisecond)
		*channel = (*timer).C
		return
	}
	if !(*timer).Stop() {
		select {
		case <-(*timer).C:
		default:
		}
	}
	(*timer).Reset(50 * time.Millisecond)
	*channel = (*timer).C
}

func (w *Watcher) flushLinuxEvents(ctx context.Context, notifier *fsnotify.Watcher, watched map[string]struct{}, root string, pending map[string]fsnotify.Op) error {
	if len(pending) == 0 {
		return nil
	}
	for path, op := range pending {
		if err := w.applyLinuxEvent(ctx, notifier, watched, root, fsnotify.Event{Name: path, Op: op}); err != nil {
			return err
		}
	}
	clear(pending)
	return nil
}

func addWatchTree(notifier *fsnotify.Watcher, watched map[string]struct{}, root string) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if filepath.Clean(path) == filepath.Clean(root) {
				return walkErr
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		path = filepath.Clean(path)
		if _, ok := watched[path]; ok {
			return nil
		}
		if err := notifier.Add(path); err != nil {
			return fmt.Errorf("watch directory %q: %w", path, err)
		}
		watched[path] = struct{}{}
		return nil
	})
	if err != nil {
		if isWatchLimitError(err) {
			return fmt.Errorf("inotify watch limit reached: %w\nhint: increase fs.inotify.max_user_watches or scan a smaller root", err)
		}
		return err
	}
	return nil
}

func (w *Watcher) applyLinuxEvent(ctx context.Context, notifier *fsnotify.Watcher, watched map[string]struct{}, root string, event fsnotify.Event) error {
	path := filepath.Clean(event.Name)
	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		return w.removeLinuxPath(ctx, watched, path)
	}

	if event.Has(fsnotify.Create) {
		created, err := w.handleLinuxCreate(ctx, notifier, watched, path)
		if err != nil || created {
			return err
		}
	}

	if event.Has(fsnotify.Write) || event.Has(fsnotify.Chmod) || event.Has(fsnotify.Create) {
		return upsertLinuxPath(ctx, w.store, root, path)
	}
	return nil
}

func (w *Watcher) removeLinuxPath(ctx context.Context, watched map[string]struct{}, path string) error {
	if _, isDir := watched[path]; isDir {
		if err := deleteWatchedPrefix(ctx, w.store, path); err != nil {
			return err
		}
		removeWatchedPrefix(watched, path)
		return nil
	}
	return deleteWatchedPath(ctx, w.store, path)
}

func (w *Watcher) handleLinuxCreate(ctx context.Context, notifier *fsnotify.Watcher, watched map[string]struct{}, path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, err
	}
	if !info.IsDir() {
		return false, nil
	}
	if err := addWatchTree(notifier, watched, path); err != nil {
		return true, err
	}
	return true, w.scanChangedDirectory(ctx, path)
}

func (w *Watcher) scanChangedDirectory(ctx context.Context, path string) error {
	exclude := w.exclude
	if len(exclude) == 0 {
		exclude = scanner.DefaultExcludes()
	}
	_, err := (scanner.Runner{
		Store:   w.store,
		Workers: scanner.DefaultWorkerCount(),
		Batch:   2000,
		Exclude: exclude,
		Backend: scanner.BackendWalk,
	}).Scan(ctx, []string{path})
	if err == nil {
		err = recalculateWatchedDirectories(ctx, w.store, []string{path})
	}
	return err
}

func upsertLinuxPath(ctx context.Context, store *db.Store, root, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	entry := db.NewEntryFromPath(root, path, info.Size(), info.ModTime(), info.IsDir())
	for attempt := 0; attempt < 3; attempt++ {
		if err := upsertWatchedEntries(ctx, store, []db.Entry{entry}); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	return upsertWatchedEntries(ctx, store, []db.Entry{entry})
}

func removeWatchedPrefix(watched map[string]struct{}, prefix string) {
	prefix = filepath.Clean(prefix)
	for path := range watched {
		rel, err := filepath.Rel(prefix, path)
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			delete(watched, path)
		}
	}
}

func isWatchLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no space left on device") || strings.Contains(msg, "too many open files")
}
