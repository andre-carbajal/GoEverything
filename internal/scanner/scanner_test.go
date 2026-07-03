package scanner

import (
	"path/filepath"
	"testing"
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
}
