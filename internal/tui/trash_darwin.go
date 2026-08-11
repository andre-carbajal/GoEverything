//go:build darwin

package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func moveToTrash(path string) error {
	script := `tell application "Finder" to delete POSIX file ` + appleScriptQuote(path)
	osascript := fixedExecutable("/usr/bin/osascript")
	if osascript == "" {
		return fmt.Errorf("move to trash: %w", errors.New("/usr/bin/osascript is unavailable"))
	}
	return exec.Command(osascript, "-e", script).Run()
}

func appleScriptQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
