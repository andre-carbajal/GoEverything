//go:build linux

package watcher

import (
	"errors"
	"strings"
	"testing"
)

func TestLinuxPermissionHintDoesNotMentionFullDiskAccess(t *testing.T) {
	err := WithPermissionHint(errors.New("permission denied"))
	if err == nil {
		t.Fatal("expected permission hint")
	}
	message := err.Error()
	if strings.Contains(message, "Full Disk Access") || !strings.Contains(message, "ACL") {
		t.Fatalf("unexpected Linux permission hint: %s", message)
	}
}

func TestSystemdWatchPathsUseUserConfigDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	unitPath, err := systemdWatchUnitPath()
	if err != nil {
		t.Fatalf("unit path: %v", err)
	}
	if !strings.HasSuffix(unitPath, "systemd/user/ge-watch.service") {
		t.Fatalf("unexpected unit path: %s", unitPath)
	}
	stdout, stderr, err := PersistentWatchLogPaths()
	if err != nil {
		t.Fatalf("log paths: %v", err)
	}
	if !strings.HasSuffix(stdout, "ge/watch.out.log") || !strings.HasSuffix(stderr, "ge/watch.err.log") {
		t.Fatalf("unexpected log paths: %s %s", stdout, stderr)
	}
}

func TestSystemdQuoteEscapesArguments(t *testing.T) {
	quoted := systemdQuote(`/tmp/a "quoted"`)
	if quoted != `"/tmp/a \"quoted\""` {
		t.Fatalf("unexpected systemd quote: %s", quoted)
	}
}

func TestSystemdUnitContainsWatchCommand(t *testing.T) {
	unit := systemdUnitContents("/usr/bin/ge", "/home/test", "/home/test/ge.db", "/tmp/out.log", "/tmp/err.log")
	for _, expected := range []string{
		"Description=GoEverything filesystem watcher",
		`ExecStart="/usr/bin/ge" watch --root "/home/test" --db "/home/test/ge.db"`,
		"Restart=on-failure",
		"StandardOutput=append:\"/tmp/out.log\"",
	} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("unit missing %q:\n%s", expected, unit)
		}
	}
}
