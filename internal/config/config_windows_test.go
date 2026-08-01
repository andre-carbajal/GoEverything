//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

func TestExpandPathSupportsWindowsDrivePaths(t *testing.T) {
	got, err := ExpandPath(`C:\Users\me\Documents`)
	if err != nil {
		t.Fatalf("expand path: %v", err)
	}
	if filepath.VolumeName(got) != "C:" || got != filepath.Clean(`C:\Users\me\Documents`) {
		t.Fatalf("unexpected Windows path: %q", got)
	}
}
