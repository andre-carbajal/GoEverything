package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSearchFTSAndWildcard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	entries := []Entry{
		NewEntryFromPath("/tmp", "/tmp/my_report.txt", 10, time.Now(), false),
		NewEntryFromPath("/tmp", "/tmp/another.log", 20, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := store.Search(ctx, "my_rep", 10, 0)
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(res) != 1 || res[0].Name != "my_report.txt" {
		t.Fatalf("unexpected fts results: %+v", res)
	}

	res, err = store.Search(ctx, "*report*", 10, 0)
	if err != nil {
		t.Fatalf("wildcard search: %v", err)
	}
	if len(res) != 1 || res[0].Name != "my_report.txt" {
		t.Fatalf("unexpected wildcard results: %+v", res)
	}
}
