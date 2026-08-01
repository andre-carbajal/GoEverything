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
	waitForWatch(t, func() bool {
		results, searchErr := store.Search(context.Background(), "created-linux", 10, 0)
		return searchErr == nil && len(results) == 1
	})
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	waitForWatch(t, func() bool {
		results, searchErr := store.Search(context.Background(), "created-linux", 10, 0)
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
	waitForWatch(t, func() bool {
		results, searchErr := store.Search(context.Background(), "nested-linux", 10, 0)
		return searchErr == nil && len(results) == 1
	})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove nested dir: %v", err)
	}
	waitForWatch(t, func() bool {
		results, searchErr := store.Search(context.Background(), "nested-linux", 10, 0)
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

func waitForWatch(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for watcher update")
}
