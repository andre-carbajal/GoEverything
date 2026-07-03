//go:build windows

package scanner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildNTFSPath(t *testing.T) {
	records := map[uint64]ntfsRecord{
		5: {frn: 5, parent: 5, name: "."},
		6: {frn: 6, parent: 5, name: "Users", isDir: true},
		7: {frn: 7, parent: 6, name: "andre", isDir: true},
		8: {frn: 8, parent: 7, name: "note.txt"},
	}

	got, ok := buildNTFSPath(`C:\`, 8, records, map[uint64]string{}, map[uint64]bool{})
	if !ok {
		t.Fatalf("expected path reconstruction to succeed")
	}
	want := filepath.Join(`C:\`, "Users", "andre", "note.txt")
	if got != want {
		t.Fatalf("unexpected path: want=%q got=%q", want, got)
	}
}

func TestDiscoverRootsWindowsShape(t *testing.T) {
	roots := DiscoverRoots()
	if len(roots) == 0 {
		t.Fatalf("expected at least one root")
	}
	for _, root := range roots {
		if len(root) != 3 || root[1:] != `:\` || root[:1] != strings.ToUpper(root[:1]) {
			t.Fatalf("unexpected Windows root shape: %q", root)
		}
	}
}
