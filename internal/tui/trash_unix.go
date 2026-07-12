//go:build !darwin && !windows

package tui

import (
	"fmt"
	"os/exec"
)

func moveToTrash(path string) error {
	if err := exec.Command("gio", "trash", "--", path).Run(); err == nil {
		return nil
	}
	if err := exec.Command("trash-put", path).Run(); err != nil {
		return fmt.Errorf("move to trash (install gio or trash-cli): %w", err)
	}
	return nil
}
