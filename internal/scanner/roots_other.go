//go:build !darwin && !windows

package scanner

func DiscoverRoots() []string {
	return []string{"/"}
}
