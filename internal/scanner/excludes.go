package scanner

import "runtime"

func DefaultExcludes() []string {
	common := []string{
		".git",
		"node_modules",
	}
	if runtime.GOOS == "windows" {
		return append(common,
			"$Recycle.Bin",
			"System Volume Information",
			"AppData/Local/Temp/*",
			"AppData/Local/Microsoft/Windows/INetCache/*",
		)
	}
	return append(common,
		".DS_Store",
		"Library/Caches/*",
		"Library/Logs/*",
		".Trash",
	)
}
