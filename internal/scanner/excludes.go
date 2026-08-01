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
	if runtime.GOOS == "linux" {
		return append(common,
			".cache",
			".local/share/Trash",
			".Trash-*",
			"lost+found",
		)
	}
	return append(common,
		".DS_Store",
		"Library/Caches/*",
		"Library/Logs/*",
		".Trash",
	)
}
