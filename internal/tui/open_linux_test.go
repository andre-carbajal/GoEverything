//go:build linux

package tui

import (
	"strings"
	"testing"
)

func TestStartOpenCommandReportsMissingDesktopOpener(t *testing.T) {
	t.Setenv("PATH", "")
	err := startOpenCommand("/tmp/file.txt", false)
	if err == nil || !strings.Contains(err.Error(), "no desktop opener") {
		t.Fatalf("expected missing opener error, got %v", err)
	}
}
