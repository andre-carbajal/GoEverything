package scanner

import "testing"

func TestSortRootsKeepsHomeFirstAndDeduplicates(t *testing.T) {
	got := sortRoots([]string{"/", "~", "/", "~/", "/tmp/../tmp"})
	if len(got) != 3 || got[0] != "~" {
		t.Fatalf("unexpected roots: %v", got)
	}
}
