//go:build linux

package watcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"goeverything/internal/db"
	"goeverything/internal/scanner"
)

func TestLinuxWatcherIndexesCreatedFile(t *testing.T) {
	root := t.TempDir()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchErr := make(chan error, 1)
	go func() { watchErr <- New(store).Run(ctx, root) }()
	time.Sleep(100 * time.Millisecond)

	path := filepath.Join(root, "created-linux.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	waitForWatch(t, ctx, watchErr, func() bool {
		results, searchErr := store.SearchAdvanced(ctx, db.SearchOptions{Query: "created-linux", Limit: 10, Offset: 0})
		return searchErr == nil && len(results) == 1
	})
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	waitForWatch(t, ctx, watchErr, func() bool {
		results, searchErr := store.SearchAdvanced(ctx, db.SearchOptions{Query: "created-linux", Limit: 10, Offset: 0})
		return searchErr == nil && len(results) == 0
	})

	cancel()
	select {
	case err := <-watchErr:
		if err != nil {
			t.Fatalf("watcher: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestLinuxWatcherIndexesDirectoryContents(t *testing.T) {
	root := t.TempDir()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "watch.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchErr := make(chan error, 1)
	go func() { watchErr <- New(store).Run(ctx, root) }()
	time.Sleep(100 * time.Millisecond)

	dir := filepath.Join(root, "new-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make dir: %v", err)
	}
	path := filepath.Join(dir, "nested-linux.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	waitForWatch(t, ctx, watchErr, func() bool {
		results, searchErr := store.SearchAdvanced(ctx, db.SearchOptions{Query: "nested-linux", Limit: 10, Offset: 0})
		return searchErr == nil && len(results) == 1
	})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove nested dir: %v", err)
	}
	waitForWatch(t, ctx, watchErr, func() bool {
		results, searchErr := store.SearchAdvanced(ctx, db.SearchOptions{Query: "nested-linux", Limit: 10, Offset: 0})
		return searchErr == nil && len(results) == 0
	})

	cancel()
	select {
	case err := <-watchErr:
		if err != nil {
			t.Fatalf("watcher: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestLinuxWatcherReportsOverflow(t *testing.T) {
	if !errors.Is(fsnotify.ErrEventOverflow, fsnotify.ErrEventOverflow) {
		t.Fatal("expected fsnotify overflow sentinel")
	}
	if !isWatchLimitError(errors.New("no space left on device")) {
		t.Fatal("expected inotify limit error")
	}
}

func waitForWatch(t *testing.T, ctx context.Context, watchErr <-chan error, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		checkWatcherState(t, ctx, watchErr)
		time.Sleep(50 * time.Millisecond)
	}
	checkWatcherState(t, ctx, watchErr)
	t.Fatal("timed out waiting for watcher update")
}

func TestLinuxWatcherMaintainsDirectorySizes(t *testing.T) {
	root := t.TempDir()
	initial := filepath.Join(root, "initial.bin")
	if err := os.WriteFile(initial, []byte("12"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "watcher-sizes.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = store.Close() }()
	if _, err := (scanner.Runner{Store: store, Backend: scanner.BackendWalk}).Scan(context.Background(), []string{root}); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchErr := make(chan error, 1)
	go func() { watchErr <- New(store).Run(ctx, root) }()
	time.Sleep(100 * time.Millisecond)

	waitForDirectorySize(t, ctx, watchErr, store, root, 2)
	created := filepath.Join(root, "created.bin")
	if err := os.WriteFile(created, []byte("1234567"), 0o644); err != nil {
		t.Fatalf("write created file: %v", err)
	}
	waitForDirectorySize(t, ctx, watchErr, store, root, 9)

	if err := os.WriteFile(created, []byte("12345678901"), 0o644); err != nil {
		t.Fatalf("modify created file: %v", err)
	}
	waitForDirectorySize(t, ctx, watchErr, store, root, 13)

	if err := os.Remove(created); err != nil {
		t.Fatalf("remove created file: %v", err)
	}
	waitForDirectorySize(t, ctx, watchErr, store, root, 2)

	cancel()
	select {
	case err := <-watchErr:
		if err != nil {
			t.Fatalf("watcher: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop after cancellation")
	}
}

func waitForDirectorySize(t *testing.T, ctx context.Context, watchErr <-chan error, store *db.Store, path string, want int64) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		results, err := store.SearchAdvanced(ctx, db.SearchOptions{
			Query:    "*",
			OnlyDirs: true,
			Limit:    1000,
		})
		if err == nil {
			for _, entry := range results {
				if filepath.Clean(entry.Path) == filepath.Clean(path) && entry.Size == want {
					return
				}
			}
		}
		checkWatcherState(t, ctx, watchErr)
		time.Sleep(25 * time.Millisecond)
	}
	checkWatcherState(t, ctx, watchErr)
	t.Fatalf("directory %q did not reach size %d", path, want)
}

func checkWatcherState(t *testing.T, ctx context.Context, watchErr <-chan error) {
	t.Helper()
	select {
	case err := <-watchErr:
		if err == nil {
			t.Fatal("watcher stopped unexpectedly")
		}
		t.Fatalf("watcher: %v", err)
	case <-ctx.Done():
		t.Fatalf("watch context canceled: %v", ctx.Err())
	default:
	}
}
