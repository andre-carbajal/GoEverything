//go:build !darwin && !windows

package watcher

import (
	"context"
	"errors"
)

func (w *Watcher) Run(_ context.Context, _ string) error {
	return errors.New("watch command currently supports macOS and Windows only")
}
