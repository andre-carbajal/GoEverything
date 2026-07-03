//go:build windows

package scanner

import (
	"sort"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func DiscoverRoots() []string {
	buf := make([]uint16, 254)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil || n == 0 {
		return []string{`C:\`}
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n)
		n, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
		if err != nil || n == 0 {
			return []string{`C:\`}
		}
	}

	roots := make([]string, 0)
	start := 0
	for i := 0; i < int(n); i++ {
		if buf[i] != 0 {
			continue
		}
		if i > start {
			root := string(utf16.Decode(buf[start:i]))
			rootType := windows.GetDriveType(windows.StringToUTF16Ptr(root))
			if rootType == windows.DRIVE_FIXED || rootType == windows.DRIVE_REMOVABLE {
				roots = append(roots, strings.ToUpper(root[:1])+root[1:])
			}
		}
		start = i + 1
	}
	if len(roots) == 0 {
		return []string{`C:\`}
	}
	sort.Strings(roots)
	return roots
}
