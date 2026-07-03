//go:build !windows

package scanner

import (
	"context"
	"errors"

	"goeverything/internal/db"
)

type ntfsBackend struct{}

func (b ntfsBackend) Scan(ctx context.Context, roots []string, emit func(db.Entry) error, progress scanProgress) error {
	return errors.New("ntfs backend is only supported on Windows")
}
