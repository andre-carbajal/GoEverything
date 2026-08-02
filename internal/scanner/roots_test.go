package scanner

import (
	"path/filepath"
	"testing"
)

func TestSortRootsKeepsHomeFirstAndDeduplicates(t *testing.T) {
	got := sortRoots([]string{"/", "~", "/", "~/", "/tmp/../tmp"})
	if len(got) != 3 || got[0] != "~" {
		t.Fatalf("unexpected roots: %v", got)
	}
}

func TestDeduplicateRootsRemovesNestedPaths(t *testing.T) {
	got := DeduplicateRoots([]string{"/", "/home", "/home/user", "/mnt/data"})
	want := []string{filepath.Clean("/")}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected roots: want=%v got=%v", want, got)
	}
}
