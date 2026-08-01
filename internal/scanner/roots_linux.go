//go:build linux

package scanner

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverRoots returns the home directory, system root, and accessible
// non-virtual mounts. Linux exposes virtual filesystems in its mount table;
// those are intentionally omitted from the quick locations.
func DiscoverRoots() []string {
	roots := []string{"~", string(filepath.Separator)}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return sortRoots(roots)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		mountpoint, fsType, ok := parseMountInfoLine(scanner.Text())
		if !ok || isLinuxPseudoFilesystem(fsType) || !accessibleDirectory(mountpoint) {
			continue
		}
		roots = append(roots, mountpoint)
	}
	return sortRoots(roots)
}

func parseMountInfoLine(line string) (mountpoint, fsType string, ok bool) {
	fields := strings.Fields(line)
	separator := -1
	for i, field := range fields {
		if field == "-" {
			separator = i
			break
		}
	}
	if separator < 5 || separator+1 >= len(fields) {
		return "", "", false
	}
	mountpoint = decodeMountInfoField(fields[4])
	if mountpoint == "" || !filepath.IsAbs(mountpoint) {
		return "", "", false
	}
	return filepath.Clean(mountpoint), fields[separator+1], true
}

func decodeMountInfoField(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+3 >= len(value) {
			out.WriteByte(value[i])
			continue
		}
		octal := value[i+1 : i+4]
		if octal[0] < '0' || octal[0] > '7' || octal[1] < '0' || octal[1] > '7' || octal[2] < '0' || octal[2] > '7' {
			out.WriteByte(value[i])
			continue
		}
		out.WriteByte(byte((octal[0]-'0')*64 + (octal[1]-'0')*8 + (octal[2] - '0')))
		i += 3
	}
	return out.String()
}

func accessibleDirectory(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	_, err = file.Readdirnames(1)
	return err == nil || err == io.EOF
}

func isLinuxPseudoFilesystem(fsType string) bool {
	switch strings.ToLower(fsType) {
	case "autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs", "efivarfs", "fusectl", "hugetlbfs", "mqueue", "pstore", "proc", "securityfs", "sysfs", "tracefs":
		return true
	default:
		return false
	}
}

func linuxMountInfo(r io.Reader) (map[string]string, error) {
	mounts := make(map[string]string)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		mountpoint, fsType, ok := parseMountInfoLine(scanner.Text())
		if ok {
			mounts[mountpoint] = fsType
		}
	}
	return mounts, scanner.Err()
}
