//go:build !darwin && !windows

package tui

import (
	"errors"
	"fmt"
	"os/exec"
)

func moveToTrash(path string) error {
	if gio := fixedCommandPath(gioName); gio != "" {
		if err := exec.Command(gio, "trash", "--", path).Run(); err == nil {
			return nil
		}
	}
	trashPut := fixedCommandPath("trash-put")
	if trashPut == "" {
		return fmt.Errorf("move to trash (install gio or trash-cli): %w", errors.New("no fixed trash command found"))
	}
	if err := exec.Command(trashPut, path).Run(); err != nil {
		return fmt.Errorf("move to trash (install gio or trash-cli): %w", err)
	}
	return nil
}
