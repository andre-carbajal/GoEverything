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
	if w.store == nil {
		return errors.New("watcher store is required")
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("watch root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return fmt.Errorf("open watch root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("watch root %q is not a directory", absRoot)
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
	pending := make(map[string]fsnotify.Op)
	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batch := pending
		pending = make(map[string]fsnotify.Op)
		for path, op := range batch {
			if err := w.applyLinuxEvent(ctx, notifier, watched, absRoot, fsnotify.Event{Name: path, Op: op}); err != nil {
				return err
			}
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-notifier.Errors:
			if !ok {
				return errors.New("inotify error channel closed")
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				return fmt.Errorf("inotify events were lost: %w\nhint: run ge scan --root %q to reconcile the index", err, absRoot)
			}
			if isWatchLimitError(err) {
				return fmt.Errorf("inotify watch limit reached: %w\nhint: increase fs.inotify.max_user_watches or scan a smaller root", err)
			}
			return fmt.Errorf("inotify watcher error: %w", err)
		case event, ok := <-notifier.Events:
			if !ok {
				return errors.New("inotify event channel closed")
			}
			path := filepath.Clean(event.Name)
			pending[path] |= event.Op
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(50 * time.Millisecond)
				debounceC = debounceTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(50 * time.Millisecond)
			}
		case <-debounceC:
			if err := flush(); err != nil {
				return err
			}
			debounceC = nil
		}
	}
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
		if _, isDir := watched[path]; isDir {
			if err := w.store.DeleteByPrefix(ctx, path); err != nil {
				return err
			}
			removeWatchedPrefix(watched, path)
		} else if err := w.store.DeleteByPath(ctx, path); err != nil {
			return err
		}
		return nil
	}

	if event.Has(fsnotify.Create) {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			if err := addWatchTree(notifier, watched, path); err != nil {
				return err
			}
			return w.scanChangedDirectory(ctx, path)
		}
	}

	if event.Has(fsnotify.Write) || event.Has(fsnotify.Chmod) || event.Has(fsnotify.Create) {
		return upsertLinuxPath(ctx, w.store, root, path)
	}
	return nil
}

func (w *Watcher) scanChangedDirectory(ctx context.Context, path string) error {
	exclude := w.exclude
	if len(exclude) == 0 {
		exclude = scanner.DefaultExcludes()
	}
	_, err := (scanner.Runner{
		Indexer: w.store,
		Workers: scanner.DefaultWorkerCount(),
		Batch:   2000,
		Exclude: exclude,
		Backend: scanner.BackendWalk,
	}).Scan(ctx, []string{path})
	return err
}

func upsertLinuxPath(ctx context.Context, store IndexStore, root, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	entry := db.NewEntryFromPath(root, path, info.Size(), info.ModTime(), info.IsDir())
	for attempt := 0; attempt < 3; attempt++ {
		if err := store.UpsertBatch(ctx, []db.Entry{entry}); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	return store.UpsertBatch(ctx, []db.Entry{entry})
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
