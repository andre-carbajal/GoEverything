package watcher

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"goeverything/internal/db"
)

type Watcher struct {
	store   *db.Store
	exclude []string
}

func New(store *db.Store, exclude ...string) *Watcher {
	return &Watcher{store: store, exclude: append([]string(nil), exclude...)}
}

func WithPermissionHint(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") {
		switch runtime.GOOS {
		case "windows":
			return fmt.Errorf("%w\nhint: run the terminal as Administrator or scan a user-owned folder", err)
		case "linux":
			return fmt.Errorf("%w\nhint: check Unix permissions/ACLs, mount options, and scan a folder owned by the current user", err)
		default:
			return fmt.Errorf("%w\nhint: grant Full Disk Access to this terminal/app in System Settings > Privacy & Security > Full Disk Access", err)
		}
	}
	return err
}

func upsertWatchedEntries(ctx context.Context, store *db.Store, entries []db.Entry) error {
	return store.UpsertBatchWithDirectorySizes(ctx, entries)
}

func deleteWatchedPath(ctx context.Context, store *db.Store, path string) error {
	return store.DeleteByPathWithDirectorySize(ctx, path)
}

func deleteWatchedPrefix(ctx context.Context, store *db.Store, prefix string) error {
	return store.DeleteByPrefixWithDirectorySize(ctx, prefix)
}

func deleteWatchedPathAndDescendants(ctx context.Context, store *db.Store, path string) error {
	return deleteWatchedPrefix(ctx, store, path)
}

func recalculateWatchedDirectories(ctx context.Context, store *db.Store, roots []string) error {
	return store.RecalculateDirectorySizes(ctx, roots)
}
