//go:build windows

package tui

import "testing"

func TestLocationCompletionPartsHandleWindowsDriveRoots(t *testing.T) {
	parent, prefix := locationCompletionParts(`C:\Users`)
	if parent != `C:\` || prefix != "Users" {
		t.Fatalf("unexpected completion parts: parent=%q prefix=%q", parent, prefix)
	}

	parent, prefix = locationCompletionParts(`C:\`)
	if parent != `C:\` || prefix != "" {
		t.Fatalf("unexpected drive-root completion parts: parent=%q prefix=%q", parent, prefix)
	}
}

func TestLocationCompletionPartsHandleWindowsNestedFolder(t *testing.T) {
	parent, prefix := locationCompletionParts(`C:\Users\name\Documents`)
	if parent != `C:\Users\name` || prefix != "Documents" {
		t.Fatalf("unexpected nested completion parts: parent=%q prefix=%q", parent, prefix)
	}
}
