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
