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
	if cfg.DeleteMode != DeleteModeTrash {
		t.Fatalf("expected default delete mode %q, got %q", DeleteModeTrash, cfg.DeleteMode)
	}

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file to exist at %s: %v", path, err)
	}
}

func TestLoadNormalizesInvalidDeleteModeToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := []byte(`{
  "db_path": "/tmp/test.db",
  "default_scan_path": "~",
  "excludes": [".git"],
  "theme": "tokyonight",
  "delete_mode": "danger",
  "auto_scan_on_start": true
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DeleteMode != DeleteModeTrash {
		t.Fatalf("expected invalid delete mode to normalize to %q, got %q", DeleteModeTrash, cfg.DeleteMode)
	}
}

func TestLoadOldConfigWithoutDeleteModeDefaultsToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := []byte(`{
  "db_path": "/tmp/test.db",
  "default_scan_path": "~",
  "excludes": [".git"],
  "theme": "tokyonight",
  "auto_scan_on_start": true
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DeleteMode != DeleteModeTrash {
		t.Fatalf("expected missing delete mode to default to %q, got %q", DeleteModeTrash, cfg.DeleteMode)
	}
}
