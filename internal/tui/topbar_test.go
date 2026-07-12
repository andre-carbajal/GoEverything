package tui

import (
	"strings"
	"testing"
	"time"

	"goeverything/internal/config"
)

func TestTopBarShowsScanDuration(t *testing.T) {
	t.Parallel()
	m := newTestModel(t, config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
	})
	m.lastMetrics.Elapsed = 2 * time.Second
	out := m.renderTopBar()
	if !strings.Contains(out, "scan took 2s") {
		t.Fatalf("expected 'scan took 2s' in top bar, got:\n%s", out)
	}
	if strings.Contains(out, "last scan") || strings.Contains(out, "ago") {
		t.Fatalf("unexpected old wording in top bar: %s", out)
	}
}
