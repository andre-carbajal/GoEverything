//go:build linux

package scanner

import (
	"strings"
	"testing"
)

func TestParseMountInfoLineDecodesEscapedMountpoint(t *testing.T) {
	line := `36 25 0:31 / /mnt/My\040Drive rw,relatime - ext4 /dev/sda rw`
	mountpoint, fsType, ok := parseMountInfoLine(line)
	if !ok {
		t.Fatal("expected mountinfo line to parse")
	}
	if mountpoint != "/mnt/My Drive" || fsType != "ext4" {
		t.Fatalf("unexpected mount: %q %q", mountpoint, fsType)
	}
}

func TestLinuxMountInfoSeparatesVirtualFilesystems(t *testing.T) {
	data := strings.NewReader(`1 0 0:1 / / rw - rootfs rootfs rw
2 1 0:2 / /proc rw - proc proc rw
3 1 0:3 / /data rw - ext4 /dev/sda rw
`)
	mounts, err := linuxMountInfo(data)
	if err != nil {
		t.Fatalf("parse mountinfo: %v", err)
	}
	if !isLinuxPseudoFilesystem(mounts["/proc"]) {
		t.Fatal("expected /proc to be identified as virtual")
	}
	if isLinuxPseudoFilesystem(mounts["/data"]) {
		t.Fatal("did not expect ext4 to be identified as virtual")
	}
}

func TestDiscoverRootsIncludesSystemAndHome(t *testing.T) {
	got := DiscoverRoots()
	seen := make(map[string]bool, len(got))
	for _, root := range got {
		seen[root] = true
	}
	if !seen["~"] || !seen["/"] {
		t.Fatalf("expected home and system roots, got %v", got)
	}
}

func TestLinuxMountFilterSkipsVirtualMountsUnderSystemRoot(t *testing.T) {
	filter := newMountFilter([]string{"/"})
	if filter == nil {
		t.Skip("/proc/self/mountinfo unavailable")
	}
	if !filter("/", "/proc") {
		t.Fatal("expected /proc to be skipped while scanning /")
	}
}
