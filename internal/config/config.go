package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goeverything/internal/scanner"
)

const (
	defaultTheme       = "tokyonight"
	ScanModeHome       = "home"
	ScanModeConfigured = "configured_roots"
	homeToken          = "~"
)

type Config struct {
	DBPath          string   `json:"db_path"`
	Roots           []string `json:"roots"`
	Excludes        []string `json:"excludes"`
	Theme           string   `json:"theme"`
	DefaultScanMode string   `json:"default_scan_mode"`
	AutoScanOnStart bool     `json:"auto_scan_on_start"`
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Config{}, fmt.Errorf("cannot create config dir %q: %w", filepath.Dir(path), err)
	}

	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if saveErr := Save(cfg); saveErr != nil {
				return Config{}, saveErr
			}
			return cfg, nil
		}
		return Config{}, err
	}

	if len(data) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.normalize()
	if err := Save(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(cfg Config) error {
	cfg.normalize()
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ge"), nil
}

func defaults() Config {
	dbPath, _ := defaultDBPath()
	return Config{
		DBPath:          dbPath,
		Roots:           []string{homeToken},
		Excludes:        scanner.DefaultExcludes(),
		Theme:           defaultTheme,
		DefaultScanMode: ScanModeHome,
		AutoScanOnStart: true,
	}
}

func defaultDBPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "goeverything.db"), nil
}

func (c *Config) normalize() {
	if c.DBPath == "" {
		dbPath, err := defaultDBPath()
		if err == nil {
			c.DBPath = dbPath
		}
	}
	if len(c.Roots) == 0 {
		c.Roots = []string{homeToken}
	}
	if len(c.Excludes) == 0 {
		c.Excludes = scanner.DefaultExcludes()
	}
	if c.Theme == "" {
		c.Theme = defaultTheme
	}
	mode := strings.ToLower(strings.TrimSpace(c.DefaultScanMode))
	if mode != ScanModeHome && mode != ScanModeConfigured {
		c.DefaultScanMode = ScanModeHome
	}
}

func ExpandPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", errors.New("path is required")
	}

	if p == homeToken || strings.HasPrefix(p, homeToken+"/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == homeToken {
			return home, nil
		}
		p = filepath.Join(home, strings.TrimPrefix(p, homeToken+"/"))
	}
	return filepath.Clean(p), nil
}

func ResolveRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		roots = []string{homeToken}
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		expanded, err := ExpandPath(root)
		if err != nil {
			return nil, err
		}
		out = append(out, expanded)
	}
	return out, nil
}
