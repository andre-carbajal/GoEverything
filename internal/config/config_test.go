package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathUsesDotConfigGe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	want := filepath.Join(home, ".config", "ge", "config.json")
	if got != want {
		t.Fatalf("unexpected path:\nwant=%s\ngot=%s", want, got)
	}
}

func TestLoadCreatesDefaultConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.AutoScanOnStart {
		t.Fatalf("expected auto scan on start=true")
	}
	if cfg.DefaultScanPath != "~" {
		t.Fatalf("unexpected default scan path: %s", cfg.DefaultScanPath)
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist at %s: %v", path, err)
	}
}
