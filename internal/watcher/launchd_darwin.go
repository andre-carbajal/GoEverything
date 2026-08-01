//go:build darwin

package watcher

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"text/template"
)

const launchdLabel = "com.ge.watch"

func InstallPersistentWatch(executable, root, dbPath string) (string, error) {
	plistPath, err := launchAgentPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", err
	}

	logDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	stdoutLog := filepath.Join(logDir, "Library", "Logs", "ge", "watch.out.log")
	stderrLog := filepath.Join(logDir, "Library", "Logs", "ge", "watch.err.log")
	if err := os.MkdirAll(filepath.Dir(stdoutLog), 0o755); err != nil {
		return "", err
	}

	var out bytes.Buffer
	tpl := template.Must(template.New("plist").Parse(plistTemplate))
	data := map[string]string{
		"Label":     launchdLabel,
		"Program":   executable,
		"Root":      root,
		"DBPath":    dbPath,
		"StdoutLog": stdoutLog,
		"StderrLog": stderrLog,
	}
	if err := tpl.Execute(&out, data); err != nil {
		return "", err
	}
	if err := os.WriteFile(plistPath, out.Bytes(), 0o644); err != nil {
		return "", err
	}
	return plistPath, nil
}

func UninstallPersistentWatch() error {
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	if err := StopPersistentWatch(); err != nil {
		// ignore if not loaded
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func StartPersistentWatch() error {
	uid, err := currentUID()
	if err != nil {
		return err
	}
	plistPath, err := launchAgentPath()
	if err != nil {
		return err
	}
	cmd := exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath)
	out, err := cmd.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("already loaded")) {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, stringsTrim(string(out)))
	}
	return nil
}

func StopPersistentWatch() error {
	uid, err := currentUID()
	if err != nil {
		return err
	}
	cmd := exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := stringsTrim(string(out))
		if s == "" || bytes.Contains(out, []byte("No such process")) {
			return nil
		}
		return fmt.Errorf("launchctl bootout: %w: %s", err, s)
	}
	return nil
}

func RestartPersistentWatch() error {
	_ = StopPersistentWatch()
	return StartPersistentWatch()
}

func PersistentWatchStatus() (string, error) {
	uid, err := currentUID()
	if err != nil {
		return "", err
	}
	cmd := exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", uid, launchdLabel))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return stringsTrim(string(out)), err
	}
	return string(out), nil
}

func PersistentWatchLogPaths() (stdout string, stderr string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(home, "Library", "Logs", "ge", "watch.out.log"),
		filepath.Join(home, "Library", "Logs", "ge", "watch.err.log"),
		nil
}

func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"), nil
}

func currentUID() (int, error) {
	u, err := user.Current()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

func stringsTrim(s string) string {
	return string(bytes.TrimSpace([]byte(s)))
}

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>{{.Label}}</string>
  <key>ProgramArguments</key>
  <array>
    <string>{{.Program}}</string>
    <string>watch</string>
    <string>--root</string>
    <string>{{.Root}}</string>
    <string>--db</string>
    <string>{{.DBPath}}</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>{{.StdoutLog}}</string>
  <key>StandardErrorPath</key>
  <string>{{.StderrLog}}</string>
</dict>
</plist>
`
