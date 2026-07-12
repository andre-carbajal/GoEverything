//go:build windows

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveToTrashWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recycle-me.txt")
	if err := os.WriteFile(path, []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveToTrash(path); err != nil {
		t.Fatalf("move to Recycle Bin: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected original path to disappear, stat error: %v", err)
	}
}
