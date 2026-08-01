package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	return home
}

func TestPathUsesDotConfigGe(t *testing.T) {
	setTestHome(t)

	got, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config dir: %v", err)
	}
	want := filepath.Join(configDir, "ge", "config.json")
	if got != want {
		t.Fatalf("unexpected path:\nwant=%s\ngot=%s", want, got)
	}
}

func TestLoadCreatesDefaultConfig(t *testing.T) {
	setTestHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
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
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), "default_scan_path") || strings.Contains(string(data), "auto_scan_on_start") {
		t.Fatalf("deprecated scan settings should not be persisted: %s", data)
	}
}

func TestLoadNormalizesInvalidDeleteModeToTrash(t *testing.T) {
	setTestHome(t)

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
  "delete_mode": "danger"
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
	setTestHome(t)

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
  "theme": "tokyonight"
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

func TestLoadMigratesLegacyScanSettingsWithoutEnablingAutoScan(t *testing.T) {
	setTestHome(t)

	path, err := Path()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := []byte(`{
  "db_path": "/tmp/test.db",
  "default_scan_path": "~/Projects",
  "auto_scan_on_start": true,
  "roots": ["~"],
  "excludes": [".git"],
  "theme": "tokyonight",
  "last_search": "report"
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LastSearch != "report" {
		t.Fatalf("expected last search to survive migration, got %q", cfg.LastSearch)
	}
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if strings.Contains(string(migrated), "default_scan_path") || strings.Contains(string(migrated), "auto_scan_on_start") || strings.Contains(string(migrated), "\"roots\"") {
		t.Fatalf("legacy scan settings were persisted after migration: %s", migrated)
	}
}
