//go:build windows

package scanner

import "testing"

func TestLogicalDriveRootsUsesAccessibleDrives(t *testing.T) {
	got := logicalDriveRoots((1<<2)|(1<<3)|(1<<25), func(root string) bool {
		return root == "C:\\" || root == "Z:\\"
	})
	want := []string{"C:\\", "Z:\\"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected drive roots: want=%v got=%v", want, got)
	}
}
