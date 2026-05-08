package watcher

import (
	"context"

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
