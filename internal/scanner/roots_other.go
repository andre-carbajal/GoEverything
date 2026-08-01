//go:build !darwin && !windows && !linux

package scanner

func DiscoverRoots() []string {
	return sortRoots([]string{"~", "/"})
}
