package scanner

import "testing"

func TestExcludeMatcher(t *testing.T) {
	t.Parallel()

	matcher := newExcludeMatcher("/Users/test", []string{".git", "Library/Caches/*"})

	if !matcher("/Users/test/project/.git", true) {
		t.Fatalf("expected .git to be excluded")
	}

	if !matcher("/Users/test/Library/Caches/app/cache.db", false) {
		t.Fatalf("expected Library/Caches/* to be excluded")
	}

	if matcher("/Users/test/Documents/report.txt", false) {
		t.Fatalf("did not expect Documents/report.txt to be excluded")
	}
}
