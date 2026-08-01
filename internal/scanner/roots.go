package scanner

import (
	"path/filepath"
	"sort"
)

func sortRoots(roots []string) []string {
	seen := make(map[string]struct{}, len(roots))
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		clean := filepath.Clean(root)
		if clean == "." && root != "." {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		result = append(result, clean)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i] == "~" {
			return result[j] != "~"
		}
		if result[j] == "~" {
			return false
		}
		return result[i] < result[j]
	})
	return result
}
