//go:build linux

package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixedExecutableRequiresAbsoluteExecutablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opener")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if got := fixedExecutable("opener"); got != "" {
		t.Fatalf("relative executable unexpectedly resolved to %q", got)
	}
	if got := fixedExecutable(path); got != path {
		t.Fatalf("absolute executable: want %q, got %q", path, got)
	}
}
