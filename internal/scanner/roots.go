package scanner

import (
	"path/filepath"
	"sort"
	"strings"
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

// DeduplicateRoots removes exact and nested roots, keeping the shortest root
// that already covers a path. Callers should resolve home tokens first.
func DeduplicateRoots(roots []string) []string {
	cleaned := sortRoots(roots)
	sort.SliceStable(cleaned, func(i, j int) bool {
		if len(cleaned[i]) == len(cleaned[j]) {
			return cleaned[i] < cleaned[j]
		}
		return len(cleaned[i]) < len(cleaned[j])
	})

	result := make([]string, 0, len(cleaned))
	for _, candidate := range cleaned {
		nested := false
		for _, parent := range result {
			rel, err := filepath.Rel(parent, candidate)
			if err != nil {
				continue
			}
			if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				nested = true
				break
			}
		}
		if !nested {
			result = append(result, candidate)
		}
	}
	return result
}
