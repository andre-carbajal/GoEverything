package scanner

import (
	"os"
	"path/filepath"
	"sort"
)

func DiscoverRoots() []string {
	roots := map[string]struct{}{
		"/": {},
	}

	entries, err := os.ReadDir("/Volumes")
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			roots[filepath.Join("/Volumes", entry.Name())] = struct{}{}
		}
	}

	result := make([]string, 0, len(roots))
	for root := range roots {
		result = append(result, root)
	}
	sort.Strings(result)
	return result
}
