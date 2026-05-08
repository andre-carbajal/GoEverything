package scanner

func DefaultExcludes() []string {
	return []string{
		".git",
		"node_modules",
		".DS_Store",
		"Library/Caches/*",
		"Library/Logs/*",
		".Trash",
	}
}
