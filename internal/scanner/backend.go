package scanner

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"goeverything/internal/db"
)

type autoBackend struct {
	ntfs    ntfsBackend
	walk    walkBackend
	warning func(string)
}

func (b autoBackend) Scan(ctx context.Context, roots []string, emit func(db.Entry) error, progress scanProgress) error {
	if runtime.GOOS != "windows" {
		return b.walk.Scan(ctx, roots, emit, progress)
	}
	err := b.ntfs.Scan(ctx, roots, emit, progress)
	if err == nil {
		return nil
	}
	if b.warning != nil {
		b.warning(fmt.Sprintf("ntfs backend unavailable, falling back to walk: %v", err))
	}
	return b.walk.Scan(ctx, roots, emit, progress)
}

func ValidateBackend(backend string) error {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "", BackendAuto, BackendWalk, BackendNTFS:
		return nil
	default:
		return fmt.Errorf("unsupported scan backend %q (allowed: auto,ntfs,walk)", backend)
	}
}
