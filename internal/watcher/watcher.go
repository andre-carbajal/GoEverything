package watcher

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"goeverything/internal/db"
)

type IndexStore interface {
	UpsertBatch(ctx context.Context, entries []db.Entry) error
	DeleteByPath(ctx context.Context, path string) error
	DeleteByPrefix(ctx context.Context, prefix string) error
}

type SizeAwareIndexStore interface {
	UpsertBatchWithDirectorySizes(ctx context.Context, entries []db.Entry) error
	DeleteByPathWithDirectorySize(ctx context.Context, path string) error
	DeleteByPrefixWithDirectorySize(ctx context.Context, prefix string) error
	RecalculateDirectorySizes(ctx context.Context, roots []string) error
}

type Watcher struct {
	store   IndexStore
	exclude []string
}

func New(store IndexStore, exclude ...string) *Watcher {
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

func upsertWatchedEntries(ctx context.Context, store IndexStore, entries []db.Entry) error {
	if sizeAware, ok := store.(SizeAwareIndexStore); ok {
		return sizeAware.UpsertBatchWithDirectorySizes(ctx, entries)
	}
	return store.UpsertBatch(ctx, entries)
}

func deleteWatchedPath(ctx context.Context, store IndexStore, path string) error {
	if sizeAware, ok := store.(SizeAwareIndexStore); ok {
		return sizeAware.DeleteByPathWithDirectorySize(ctx, path)
	}
	return store.DeleteByPath(ctx, path)
}

func deleteWatchedPrefix(ctx context.Context, store IndexStore, prefix string) error {
	if sizeAware, ok := store.(SizeAwareIndexStore); ok {
		return sizeAware.DeleteByPrefixWithDirectorySize(ctx, prefix)
	}
	return store.DeleteByPrefix(ctx, prefix)
}

func deleteWatchedPathAndDescendants(ctx context.Context, store IndexStore, path string) error {
	if _, ok := store.(SizeAwareIndexStore); ok {
		return deleteWatchedPrefix(ctx, store, path)
	}
	if err := store.DeleteByPath(ctx, path); err != nil {
		return err
	}
	return store.DeleteByPrefix(ctx, path)
}

func recalculateWatchedDirectories(ctx context.Context, store IndexStore, roots []string) error {
	if sizeAware, ok := store.(SizeAwareIndexStore); ok {
		return sizeAware.RecalculateDirectorySizes(ctx, roots)
	}
	return nil
}
