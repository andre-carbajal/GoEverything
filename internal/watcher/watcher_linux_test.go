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
