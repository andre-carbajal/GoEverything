//go:build !darwin

package watcher

import (
	"context"
	"errors"
)

func (w *Watcher) Run(_ context.Context, _ string) error {
	return errors.New("watch command currently supports darwin only")
}
