package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"goeverything/internal/db"
)

func TestExcludeMatcher(t *testing.T) {
	t.Parallel()

	root := filepath.Join(string(filepath.Separator), "Users", "test")
	matcher := newExcludeMatcher(root, []string{".git", "Library/Caches/*"})

	if !matcher(filepath.Join(root, "project", ".git"), true) {
		t.Fatalf("expected .git to be excluded")
	}

	if !matcher(filepath.Join(root, "Library", "Caches", "app", "cache.db"), false) {
		t.Fatalf("expected Library/Caches/* to be excluded")
	}

	if matcher(filepath.Join(root, "Documents", "report.txt"), false) {
		t.Fatalf("did not expect Documents/report.txt to be excluded")
	}

	if !newExcludeMatcher(root, []string{".Trash-*"})(filepath.Join(root, ".Trash-1000"), true) {
		t.Fatalf("expected wildcard basename to be excluded")
	}
}

func TestRunnerPrunesDeletedFileAfterRescan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t)
	defer func() { _ = store.Close() }()

	target := filepath.Join(root, "ghost-file.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	r := Runner{Store: store, Backend: BackendWalk, Batch: 2}
	if _, err := r.Scan(ctx, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	assertSearchCount(t, store, "ghost", 1)

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove target: %v", err)
	}
	if _, err := r.Scan(ctx, []string{root}); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	assertSearchCount(t, store, "ghost", 0)
}

func TestRunnerPrunesDeletedDirectoryAfterRescan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t)
	defer func() { _ = store.Close() }()

	goneDir := filepath.Join(root, "gone")
	if err := os.MkdirAll(filepath.Join(goneDir, "nested"), 0o755); err != nil {
		t.Fatalf("make nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goneDir, "nested", "nested-note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	r := Runner{Store: store, Backend: BackendWalk, Batch: 2}
	if _, err := r.Scan(ctx, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	assertSearchCount(t, store, "nested-note", 1)

	if err := os.RemoveAll(goneDir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if _, err := r.Scan(ctx, []string{root}); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	assertSearchCount(t, store, "nested-note", 0)
}

func TestRunnerPrunesNewlyExcludedPathsAfterRescan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t)
	defer func() { _ = store.Close() }()

	excludedDir := filepath.Join(root, "excluded")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		t.Fatalf("make excluded dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(excludedDir, "old-cache.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write excluded file: %v", err)
	}

	if _, err := (Runner{Store: store, Backend: BackendWalk, Batch: 2}).Scan(ctx, []string{root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	assertSearchCount(t, store, "old-cache", 1)

	if _, err := (Runner{Store: store, Backend: BackendWalk, Batch: 2, Exclude: []string{"excluded"}}).Scan(ctx, []string{root}); err != nil {
		t.Fatalf("excluded scan: %v", err)
	}
	assertSearchCount(t, store, "old-cache", 0)
}

func TestRunnerDoesNotPruneWhenCanceled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := openTestStore(t)
	defer func() { _ = store.Close() }()

	stale := db.NewEntryFromPath(root, filepath.Join(root, "stale.txt"), 10, time.Now(), false)
	if err := store.UpsertBatch(context.Background(), []db.Entry{stale}); err != nil {
		t.Fatalf("upsert stale entry: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Runner{Store: store, Backend: BackendWalk}).Scan(ctx, []string{root})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	assertSearchCount(t, store, "stale", 1)
}

func TestRunnerDoesNotPruneWhenScanErrors(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "missing")
	store := openTestStore(t)
	defer func() { _ = store.Close() }()

	stale := db.NewEntryFromPath(root, filepath.Join(root, "stale.txt"), 10, time.Now(), false)
	if err := store.UpsertBatch(context.Background(), []db.Entry{stale}); err != nil {
		t.Fatalf("upsert stale entry: %v", err)
	}

	_, err := (Runner{Store: store, Backend: BackendWalk}).Scan(context.Background(), []string{root})
	if err == nil {
		t.Fatalf("expected scan error")
	}
	assertSearchCount(t, store, "stale", 1)
}

func TestRunnerPublishesRecursiveDirectorySizes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("make nested: %v", err)
	}
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("make empty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "one.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatalf("write one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "two.txt"), []byte("1234567"), 0o644); err != nil {
		t.Fatalf("write two: %v", err)
	}

	store := openTestStore(t)
	defer func() { _ = store.Close() }()
	if _, err := (Runner{Store: store, Backend: BackendWalk, Batch: 2}).Scan(ctx, []string{root}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	for path, want := range map[string]int64{root: 12, nested: 12, empty: 0} {
		entry := findEntry(t, store, path)
		if entry.Size != want {
			t.Fatalf("directory %q size: want %d, got %d", path, want, entry.Size)
		}
	}
}

func TestRunnerDirectorySizesRespectExclusions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "included"), 0o755); err != nil {
		t.Fatalf("make included: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "excluded"), 0o755); err != nil {
		t.Fatalf("make excluded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "included", "ok.bin"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write included: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "excluded", "ignored.bin"), []byte("123456789"), 0o644); err != nil {
		t.Fatalf("write excluded: %v", err)
	}

	store := openTestStore(t)
	defer func() { _ = store.Close() }()
	if _, err := (Runner{Store: store, Backend: BackendWalk, Exclude: []string{"excluded"}}).Scan(context.Background(), []string{root}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := findEntry(t, store, root).Size; got != 4 {
		t.Fatalf("root size: want 4, got %d", got)
	}
}

func TestRunnerDirectorySizesCountHardlinksByIndexedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	original := filepath.Join(root, "original.bin")
	link := filepath.Join(root, "hardlink.bin")
	if err := os.WriteFile(original, []byte("123456"), 0o644); err != nil {
		t.Fatalf("write original: %v", err)
	}
	if err := os.Link(original, link); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}

	store := openTestStore(t)
	defer func() { _ = store.Close() }()
	if _, err := (Runner{Store: store, Backend: BackendWalk}).Scan(context.Background(), []string{root}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := findEntry(t, store, root).Size; got != 12 {
		t.Fatalf("root size with two indexed hardlink paths: want 12, got %d", got)
	}
}

func TestRunnerDoesNotPublishDirectorySizesWhenCanceled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.bin"), []byte("1234"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	store := openTestStore(t)
	defer func() { _ = store.Close() }()
	if err := store.UpsertBatch(context.Background(), []db.Entry{
		db.NewEntryFromPath(root, root, 99, time.Now(), true),
	}); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Runner{Store: store, Backend: BackendWalk}).Scan(ctx, []string{root}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if got := findEntry(t, store, root).Size; got != 99 {
		t.Fatalf("canceled scan changed root size: want 99, got %d", got)
	}
}

func openTestStore(t *testing.T) *db.Store {
	t.Helper()

	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return store
}

func assertSearchCount(t *testing.T, store *db.Store, query string, want int) {
	t.Helper()

	got, err := store.SearchAdvanced(context.Background(), db.SearchOptions{Query: query, Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("search %q: %v", query, err)
	}
	if len(got) != want {
		t.Fatalf("search %q: expected %d results, got %+v", query, want, got)
	}
}

func findEntry(t *testing.T, store *db.Store, path string) db.Entry {
	t.Helper()
	results, err := store.SearchAdvanced(context.Background(), db.SearchOptions{
		Query:    filepath.Base(path),
		OnlyDirs: true,
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
	return db.Entry{}
}
