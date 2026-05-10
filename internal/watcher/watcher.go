package watcher

import (
	"context"
	"fmt"
	"strings"

	"goeverything/internal/db"
)

type IndexStore interface {
	UpsertBatch(ctx context.Context, entries []db.Entry) error
	DeleteByPath(ctx context.Context, path string) error
	DeleteByPrefix(ctx context.Context, prefix string) error
}

type Watcher struct {
	store IndexStore
}

func New(store IndexStore) *Watcher {
	return &Watcher{store: store}
}

func WithPermissionHint(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") {
		return fmt.Errorf("%w\nhint: grant Full Disk Access to this terminal/app in System Settings > Privacy & Security > Full Disk Access", err)
	}
	return err
}
