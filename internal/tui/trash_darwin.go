//go:build darwin

package tui

import (
	"os/exec"
	"strings"
)

func moveToTrash(path string) error {
	script := `tell application "Finder" to delete POSIX file ` + appleScriptQuote(path)
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}
