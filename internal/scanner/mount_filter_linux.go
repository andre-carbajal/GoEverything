//go:build linux

package scanner

import (
	"os"
	"path/filepath"
	"strings"
)

func newMountFilter(_ []string) func(root, path string) bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	mounts, err := linuxMountInfo(file)
	if err != nil {
		return nil
	}
	return func(root, path string) bool {
		root = filepath.Clean(root)
		path = filepath.Clean(path)
		for mountpoint, fsType := range mounts {
			if !isLinuxPseudoFilesystem(fsType) || !isWithinPath(mountpoint, path) {
				continue
			}
			if isWithinPath(mountpoint, root) {
				continue
			}
			return true
		}
		return false
	}
}

func isWithinPath(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
