//go:build linux

package watcher

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const systemdWatchUnit = "ge-watch.service"

func InstallPersistentWatch(executable, root, dbPath string) (string, error) {
	if executable == "" || root == "" || dbPath == "" {
		return "", errors.New("executable, watch root, and db path are required")
	}
	unitPath, err := systemdWatchUnitPath()
	if err != nil {
		return "", err
	}
	stdout, stderr, err := PersistentWatchLogPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(stdout), 0o755); err != nil {
		return "", err
	}
	unit := systemdUnitContents(executable, root, dbPath, stdout, stderr)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return "", err
	}
	if err := systemctlUser("daemon-reload"); err != nil {
		return "", err
	}
	return unitPath, nil
}

func systemdUnitContents(executable, root, dbPath, stdout, stderr string) string {
	return fmt.Sprintf(`[Unit]
Description=GoEverything filesystem watcher
After=default.target

[Service]
ExecStart=%s watch --root %s --db %s
Restart=on-failure
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, systemdQuote(executable), systemdQuote(root), systemdQuote(dbPath), systemdQuote(stdout), systemdQuote(stderr))
}

func UninstallPersistentWatch() error {
	_ = systemctlUser("disable", "--now", systemdWatchUnit)
	unitPath, err := systemdWatchUnitPath()
	if err != nil {
		return err
	}
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return systemctlUser("daemon-reload")
}

func StartPersistentWatch() error {
	return systemctlUser("enable", "--now", systemdWatchUnit)
}

func StopPersistentWatch() error {
	return systemctlUser("disable", "--now", systemdWatchUnit)
}

func RestartPersistentWatch() error {
	return systemctlUser("restart", systemdWatchUnit)
}

func PersistentWatchStatus() (string, error) {
	cmd, err := systemctlCommand("status", "--no-pager", systemdWatchUnit)
	if err != nil {
		return "", err
	}
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return string(bytes.TrimSpace(out)), fmt.Errorf("systemctl --user status: %w: %s", runErr, bytes.TrimSpace(out))
	}
	return string(bytes.TrimSpace(out)), nil
}

func PersistentWatchLogPaths() (stdout string, stderr string, err error) {
	base, err := systemdStateDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(base, "ge", "watch.out.log"), filepath.Join(base, "ge", "watch.err.log"), nil
}

func systemdStateDir() (string, error) {
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return base, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}

func systemdWatchUnitPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "systemd", "user", systemdWatchUnit), nil
}

func systemctlUser(args ...string) error {
	cmd, err := systemctlCommand(args...)
	if err != nil {
		return err
	}
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", args[0], runErr, bytes.TrimSpace(out))
	}
	return nil
}

func systemctlCommand(args ...string) (*exec.Cmd, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, fmt.Errorf("systemd user service unavailable: systemctl was not found; use foreground ge watch instead: %w", err)
	}
	return exec.Command("systemctl", append([]string{"--user"}, args...)...), nil
}

func systemdQuote(value string) string {
	return strconv.Quote(value)
}
