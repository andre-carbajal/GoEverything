package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestStoreRetriesBusyTransactions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	attempts := 0
	err = store.withTx(ctx, func(tx *sql.Tx) error {
		attempts++
		if attempts < 3 {
			return errors.New("SQLITE_BUSY_SNAPSHOT")
		}
		_, err := tx.ExecContext(ctx, `CREATE TABLE retry_probe (id INTEGER PRIMARY KEY)`)
		return err
	})
	if err != nil {
		t.Fatalf("transaction retry: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 transaction attempts, got %d", attempts)
	}
}

func testPath(parts ...string) string {
	all := append([]string{string(filepath.Separator)}, parts...)
	return filepath.Join(all...)
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func TestStoreSearchFTSAndWildcard(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	entries := []Entry{
		NewEntryFromPath("/tmp", "/tmp/my_report.txt", 10, time.Now(), false),
		NewEntryFromPath("/tmp", "/tmp/another.log", 20, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := store.SearchAdvanced(ctx, SearchOptions{Query: "my_rep", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("fts search: %v", err)
	}
	if len(res) != 1 || res[0].Name != "my_report.txt" {
		t.Fatalf("unexpected fts results: %+v", res)
	}

	res, err = store.SearchAdvanced(ctx, SearchOptions{Query: "*report*", Limit: 10, Offset: 0})
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
	defer func() { _ = store.Close() }()

	now := time.Now()
	root := testPath("Users", "a")
	entries := []Entry{
		NewEntryFromPath(root, filepath.Join(root, "report.txt"), 10, now, false),
		NewEntryFromPath(root, filepath.Join(root, "src", "main.go"), 20, now, false),
		NewEntryFromPath(root, filepath.Join(root, "src"), 0, now, true),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{
		Query:     "report",
		OnlyFiles: true,
		Ext:       ".txt",
		Root:      root,
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
	got, err = store.SearchAdvanced(ctx, SearchOptions{Query: "main", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(got) != 1 || got[0].Name != "main.go" {
		t.Fatalf("unexpected results after reindex: %+v", got)
	}
}

func TestStoreSearchDoesNotMatchPathSegments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	root := testPath("Users", "a")
	entries := []Entry{
		NewEntryFromPath(root, filepath.Join(root, "projects", "go", "main.go"), 20, now, false),
		NewEntryFromPath(root, filepath.Join(root, "projects", "js", "index.js"), 20, now, false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{
		Query: "*" + filepath.Join("projects", "go") + "*",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("search default: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no default path hits, got: %+v", got)
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
	defer func() { _ = store.Close() }()

	now := time.Now()
	root := testPath("Users", "a")
	goDir := filepath.Join(root, "projects", "go")
	entries := []Entry{
		NewEntryFromPath(root, filepath.Join(goDir, "main.go"), 10, now, false),
		NewEntryFromPath(root, filepath.Join(goDir, "utils.go"), 12, now, false),
		NewEntryFromPath(root, filepath.Join(root, "projects", "js", "index.js"), 14, now, false),
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

	if err := store.DeleteByPath(ctx, filepath.Join(goDir, "utils.go")); err != nil {
		t.Fatalf("delete by path: %v", err)
	}
	if err := store.DeleteByPrefix(ctx, goDir); err != nil {
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

func TestStoreUpdateDirectorySizesBatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "sizes.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	root := testPath("sizes", "root")
	nested := filepath.Join(root, "nested")
	empty := filepath.Join(root, "empty")
	entries := []Entry{
		NewEntryFromPath(root, root, 0, time.Now(), true),
		NewEntryFromPath(root, nested, 0, time.Now(), true),
		NewEntryFromPath(root, empty, 0, time.Now(), true),
		NewEntryFromPath(root, filepath.Join(nested, "one.bin"), 12, time.Now(), false),
		NewEntryFromPath(root, filepath.Join(nested, "two.bin"), 30, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.UpdateDirectorySizes(ctx, map[string]int64{root: 42, nested: 42, empty: 0}); err != nil {
		t.Fatalf("update directory sizes: %v", err)
	}

	for path, want := range map[string]int64{root: 42, nested: 42, empty: 0} {
		got := findEntryByPath(t, store, path, true)
		if got.Size != want {
			t.Fatalf("directory %q size: want %d, got %d", path, want, got.Size)
		}
	}
}

func TestStoreWatcherDirectorySizeDeltas(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "watcher-sizes.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	root := testPath("watcher", "root")
	nested := filepath.Join(root, "nested")
	target := filepath.Join(nested, "data.bin")
	entries := []Entry{
		NewEntryFromPath(root, root, 0, time.Now(), true),
		NewEntryFromPath(root, nested, 0, time.Now(), true),
		NewEntryFromPath(root, target, 10, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if err := store.UpdateDirectorySizes(ctx, map[string]int64{root: 10, nested: 10}); err != nil {
		t.Fatalf("initial sizes: %v", err)
	}

	updated := NewEntryFromPath(root, target, 25, time.Now(), false)
	if err := store.UpsertBatchWithDirectorySizes(ctx, []Entry{updated}); err != nil {
		t.Fatalf("updated file: %v", err)
	}
	if got := findEntryByPath(t, store, root, true).Size; got != 25 {
		t.Fatalf("root size after update: want 25, got %d", got)
	}
	if got := findEntryByPath(t, store, nested, true).Size; got != 25 {
		t.Fatalf("nested size after update: want 25, got %d", got)
	}

	if err := store.DeleteByPathWithDirectorySize(ctx, target); err != nil {
		t.Fatalf("delete file: %v", err)
	}
	if got := findEntryByPath(t, store, root, true).Size; got != 0 {
		t.Fatalf("root size after delete: want 0, got %d", got)
	}
	if got := findEntryByPath(t, store, nested, true).Size; got != 0 {
		t.Fatalf("nested size after delete: want 0, got %d", got)
	}
}

func TestStoreDirectoryDeleteSubtractsSubtreeSize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "subtree-sizes.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	root := testPath("subtree", "root")
	removed := filepath.Join(root, "removed")
	keep := filepath.Join(root, "keep")
	entries := []Entry{
		NewEntryFromPath(root, root, 0, time.Now(), true),
		NewEntryFromPath(root, removed, 0, time.Now(), true),
		NewEntryFromPath(root, keep, 0, time.Now(), true),
		NewEntryFromPath(root, filepath.Join(removed, "a.bin"), 7, time.Now(), false),
		NewEntryFromPath(root, filepath.Join(removed, "b.bin"), 11, time.Now(), false),
		NewEntryFromPath(root, filepath.Join(keep, "c.bin"), 5, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.UpdateDirectorySizes(ctx, map[string]int64{root: 23, removed: 18, keep: 5}); err != nil {
		t.Fatalf("initial sizes: %v", err)
	}
	if err := store.DeleteByPrefixWithDirectorySize(ctx, removed); err != nil {
		t.Fatalf("delete subtree: %v", err)
	}
	if got := findEntryByPath(t, store, root, true).Size; got != 5 {
		t.Fatalf("root size after subtree delete: want 5, got %d", got)
	}
	if got := findEntryByPath(t, store, keep, true).Size; got != 5 {
		t.Fatalf("keep size after subtree delete: want 5, got %d", got)
	}
}

func findEntryByPath(t *testing.T, store *Store, path string, onlyDir bool) Entry {
	t.Helper()
	results, err := store.SearchAdvanced(context.Background(), SearchOptions{
		Query:    filepath.Base(path),
		OnlyDirs: onlyDir,
		Limit:    100,
	})
	if err != nil {
		t.Fatalf("search %q: %v", path, err)
	}
	for _, entry := range results {
		if filepath.Clean(entry.Path) == filepath.Clean(path) {
			return entry
		}
	}
	t.Fatalf("entry %q not found in %+v", path, results)
	return Entry{}
}

func TestStoreDeleteByPrefixRemovesNestedDescendants(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	root := testPath("Users", "a")
	prefix := filepath.Join(root, "projects", "go")
	keepPath := filepath.Join(root, "projects", "js", "index.js")
	entries := []Entry{
		NewEntryFromPath(root, filepath.Join(prefix, "pkg", "main.go"), 10, now, false),
		NewEntryFromPath(root, filepath.Join(prefix, "pkg", "readme.md"), 12, now, false),
		NewEntryFromPath(root, keepPath, 14, now, false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := store.DeleteByPrefix(ctx, prefix); err != nil {
		t.Fatalf("delete by prefix: %v", err)
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{Query: "main", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search deleted nested file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected nested descendant to be deleted, got %+v", got)
	}

	got, err = store.SearchAdvanced(ctx, SearchOptions{Query: "index", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search kept file: %v", err)
	}
	if len(got) != 1 || got[0].Path != keepPath {
		t.Fatalf("expected unrelated file to remain, got %+v", got)
	}
}

func TestStoreFinishScanPrunesMissingAndKeepsProtectedPrefixes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")

	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	root := testPath("Users", "a")
	protectedDir := filepath.Join(root, "private")
	keep := NewEntryFromPath(root, filepath.Join(root, "keep.txt"), 10, now, false)
	stale := NewEntryFromPath(root, filepath.Join(root, "stale.txt"), 10, now, false)
	protected := NewEntryFromPath(root, filepath.Join(protectedDir, "secret.txt"), 10, now, false)
	if err := store.UpsertBatch(ctx, []Entry{keep, stale, protected}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	sessionID, err := store.BeginScan(ctx, []string{root})
	if err != nil {
		t.Fatalf("begin scan: %v", err)
	}
	if err := store.MarkSeenBatch(ctx, sessionID, []Entry{keep}); err != nil {
		t.Fatalf("mark seen: %v", err)
	}
	if err := store.MarkUnreadablePrefix(ctx, sessionID, protectedDir); err != nil {
		t.Fatalf("mark protected: %v", err)
	}
	if err := store.FinishScan(ctx, sessionID, []string{root}); err != nil {
		t.Fatalf("finish scan: %v", err)
	}

	for query, want := range map[string]int{"keep": 1, "secret": 1, "stale": 0} {
		got, err := store.SearchAdvanced(ctx, SearchOptions{Query: query, Limit: 10, Offset: 0})
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		if len(got) != want {
			t.Fatalf("search %q: expected %d results, got %+v", query, want, got)
		}
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
	defer func() { _ = legacySQL.Close() }()

	// Simulate legacy schema with path stored directly in entries and FTS over path+name.
	root := testPath("Users", "a")
	fullPath := filepath.Join(root, "projects", "go", "main.go")
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
		"INSERT INTO entries(name, path, ext, size, mtime, is_dir, root, indexed_at) VALUES ('main.go', " + sqlString(fullPath) + ", 'go', 10, 1, 0, " + sqlString(root) + ", 1);",
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
	defer func() { _ = store.Close() }()

	var hasPathCol int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name='path'`).Scan(&hasPathCol); err != nil {
		t.Fatalf("check migrated columns: %v", err)
	}
	if hasPathCol != 0 {
		t.Fatalf("legacy path column should not exist after migration")
	}

	got, err := store.SearchAdvanced(ctx, SearchOptions{Query: "main", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search migrated db: %v", err)
	}
	if len(got) != 1 || got[0].Path != fullPath {
		t.Fatalf("unexpected migrated search results: %+v", got)
	}
}
