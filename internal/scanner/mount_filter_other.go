//go:build !linux

package scanner

func newMountFilter(_ []string) func(root, path string) bool {
	return nil
}
