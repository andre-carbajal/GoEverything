package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
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

func TestStoreSearchAdvancedFiltersAndReindex(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entries := []Entry{
		NewEntryFromPath("/Users/a", "/Users/a/report.txt", 10, now, false),
		NewEntryFromPath("/Users/a", "/Users/a/src/main.go", 20, now, false),
		NewEntryFromPath("/Users/a", "/Users/a/src", 0, now, true),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{
		Query:     "report",
		OnlyFiles: true,
		Ext:       ".txt",
		Root:      "/Users/a",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("search advanced: %v", err)
	}
	if len(got) != 1 || got[0].Name != "report.txt" {
		t.Fatalf("unexpected search advanced results: %+v", got)
	}

	if err := store.ReindexFTS(ctx); err != nil {
		t.Fatalf("reindex fts: %v", err)
	}
	got, err = store.Search(ctx, "main", 10, 0)
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(got) != 1 || got[0].Name != "main.go" {
		t.Fatalf("unexpected results after reindex: %+v", got)
	}
}

func TestStoreSearchByPathRequiresExplicitFlag(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entries := []Entry{
		NewEntryFromPath("/Users/a", "/Users/a/projects/go/main.go", 20, now, false),
		NewEntryFromPath("/Users/a", "/Users/a/projects/js/index.js", 20, now, false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{
		Query: "*projects/go*",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("search default: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no default path hits, got: %+v", got)
	}

	got, err = store.SearchAdvanced(ctx, SearchOptions{
		Query:        "main",
		SearchInPath: true,
		PathQuery:    "*projects/go*",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("search with explicit path filter: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/Users/a/projects/go/main.go" {
		t.Fatalf("unexpected explicit path-filter results: %+v", got)
	}
}

func TestStoreDirectoryDedupAndDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entries := []Entry{
		NewEntryFromPath("/Users/a", "/Users/a/projects/go/main.go", 10, now, false),
		NewEntryFromPath("/Users/a", "/Users/a/projects/go/utils.go", 12, now, false),
		NewEntryFromPath("/Users/a", "/Users/a/projects/js/index.js", 14, now, false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var dirs int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM directories`).Scan(&dirs); err != nil {
		t.Fatalf("count dirs: %v", err)
	}
	if dirs != 2 {
		t.Fatalf("expected 2 unique directories, got %d", dirs)
	}

	if err := store.DeleteByPath(ctx, "/Users/a/projects/go/utils.go"); err != nil {
		t.Fatalf("delete by path: %v", err)
	}
	if err := store.DeleteByPrefix(ctx, "/Users/a/projects/go"); err != nil {
		t.Fatalf("delete by prefix: %v", err)
	}

	var remaining int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&remaining); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("expected 1 remaining entry, got %d", remaining)
	}
}

func TestStoreMigratesLegacySchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	legacySQL, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer legacySQL.Close()

	// Simulate legacy schema with path stored directly in entries and FTS over path+name.
	legacySchema := []string{
		`DROP TRIGGER IF EXISTS entries_ai;`,
		`DROP TRIGGER IF EXISTS entries_ad;`,
		`DROP TRIGGER IF EXISTS entries_au;`,
		`DROP TABLE IF EXISTS entries_fts;`,
		`DROP TABLE IF EXISTS entries;`,
		`DROP TABLE IF EXISTS directories;`,
		`CREATE TABLE entries (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			ext TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			is_dir INTEGER NOT NULL,
			root TEXT NOT NULL,
			indexed_at INTEGER NOT NULL
		);`,
		`CREATE VIRTUAL TABLE entries_fts USING fts5(
			name,
			path,
			content='entries',
			content_rowid='id',
			tokenize='unicode61 remove_diacritics 2',
			prefix='2 3 4'
		);`,
		`CREATE TRIGGER entries_ai AFTER INSERT ON entries BEGIN
			INSERT INTO entries_fts(rowid, name, path) VALUES (new.id, new.name, new.path);
		END;`,
		`INSERT INTO entries(name, path, ext, size, mtime, is_dir, root, indexed_at)
		 VALUES ('main.go', '/Users/a/projects/go/main.go', 'go', 10, 1, 0, '/Users/a', 1);`,
	}
	for _, stmt := range legacySchema {
		if _, err := legacySQL.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("prepare legacy schema: %v", err)
		}
	}

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer store.Close()

	var hasPathCol int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='path'`).Scan(&hasPathCol); err != nil {
		t.Fatalf("check migrated columns: %v", err)
	}
	if hasPathCol != 0 {
		t.Fatalf("legacy path column should not exist after migration")
	}

	got, err := store.Search(ctx, "main", 10, 0)
	if err != nil {
		t.Fatalf("search migrated db: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/Users/a/projects/go/main.go" {
		t.Fatalf("unexpected migrated search results: %+v", got)
	}
}
