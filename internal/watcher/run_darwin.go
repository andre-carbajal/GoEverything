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
	if w.store == nil {
		return errors.New("watcher store is required")
	}
	if root == "" {
		return errors.New("watch root is required")
	}

	absRoot, err := filepath.Abs(root)
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

			upserts := make([]db.Entry, 0, len(batch))
			for _, evt := range batch {
				path := filepath.Clean(evt.Path)

				if evt.Flags&fsevents.ItemRemoved != 0 {
					if evt.Flags&fsevents.ItemIsDir != 0 {
						if err := deleteWatchedPrefix(ctx, w.store, path); err != nil {
							return err
						}
						continue
					}
					if err := deleteWatchedPath(ctx, w.store, path); err != nil {
						return err
					}
					continue
				}

				if evt.Flags&(fsevents.ItemCreated|fsevents.ItemRenamed|fsevents.ItemModified) != 0 {
					info, statErr := os.Stat(path)
					if statErr != nil {
						continue
					}
					upserts = append(upserts, db.NewEntryFromPath(absRoot, path, info.Size(), info.ModTime(), info.IsDir()))
				}
			}

			if err := upsertWatchedEntries(ctx, w.store, upserts); err != nil {
				return err
			}
		}
	}
}
