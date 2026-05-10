package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"goeverything/internal/scanner"
)

const (
	defaultTheme = "tokyonight"
)

type Config struct {
	DBPath   string   `json:"db_path"`
	Roots    []string `json:"roots"`
	Excludes []string `json:"excludes"`
	Theme    string   `json:"theme"`
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	cfg := defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
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
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ge", "config.json"), nil
}

func defaults() Config {
	dbPath, _ := defaultDBPath()
	return Config{
		DBPath:   dbPath,
		Roots:    []string{"/"},
		Excludes: scanner.DefaultExcludes(),
		Theme:    defaultTheme,
	}
}

func defaultDBPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ge", "goeverything.db"), nil
}

func (c *Config) normalize() {
	if c.DBPath == "" {
		dbPath, err := defaultDBPath()
		if err == nil {
			c.DBPath = dbPath
		}
	}
	if len(c.Roots) == 0 {
		c.Roots = []string{"/"}
	}
	if len(c.Excludes) == 0 {
		c.Excludes = scanner.DefaultExcludes()
	}
	if c.Theme == "" {
		c.Theme = defaultTheme
	}
}
