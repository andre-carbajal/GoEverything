//go:build darwin

package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsevents"

	"goeverything/internal/db"
)

func (w *Watcher) Run(ctx context.Context, root string) error {
	absRoot, err := validateDarwinWatchRoot(w.store, root)
	if err != nil {
		return err
	}

	device, err := fsevents.DeviceForPath(absRoot)
	if err != nil {
		return fmt.Errorf("resolve fsevents device: %w", err)
	}

	stream := &fsevents.EventStream{
		Paths:   []string{absRoot},
		Latency: 250 * time.Millisecond,
		Flags: fsevents.FileEvents |
			fsevents.WatchRoot |
			fsevents.NoDefer,
		Device: device,
	}

	if err := stream.Start(); err != nil {
		return fmt.Errorf("start fsevents stream: %w", err)
	}
	defer stream.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case batch, ok := <-stream.Events:
			if !ok {
				return errors.New("fsevents stream closed")
			}
			if err := w.processDarwinBatch(ctx, absRoot, batch); err != nil {
				return err
			}
		}
	}
}

func validateDarwinWatchRoot(store *db.Store, root string) (string, error) {
	if store == nil {
		return "", errors.New("watcher store is required")
	}
	if root == "" {
		return "", errors.New("watch root is required")
	}
	return filepath.Abs(root)
}

func (w *Watcher) processDarwinBatch(ctx context.Context, root string, batch []fsevents.Event) error {
	upserts := make([]db.Entry, 0, len(batch))
	for _, evt := range batch {
		path := filepath.Clean(evt.Path)
		if evt.Flags&fsevents.ItemRemoved != 0 {
			if err := w.removeDarwinPath(ctx, path, evt.Flags&fsevents.ItemIsDir != 0); err != nil {
				return err
			}
			continue
		}
		if evt.Flags&(fsevents.ItemCreated|fsevents.ItemRenamed|fsevents.ItemModified) == 0 {
			continue
		}
		info, err := os.Stat(path)
		if err == nil {
			upserts = append(upserts, db.NewEntryFromPath(root, path, info.Size(), info.ModTime(), info.IsDir()))
		}
	}
	return upsertWatchedEntries(ctx, w.store, upserts)
}

func (w *Watcher) removeDarwinPath(ctx context.Context, path string, isDir bool) error {
	if isDir {
		return deleteWatchedPrefix(ctx, w.store, path)
	}
	return deleteWatchedPath(ctx, w.store, path)
}
