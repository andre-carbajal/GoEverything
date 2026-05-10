package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"goeverything/internal/config"
	"goeverything/internal/db"
)

func TestMenuEnterNavigatesToSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:   "/tmp/test.db",
		Roots:    []string{"/"},
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected search mode, got %v", next.mode)
	}
}

func TestNewModelStartsInSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		Roots:           []string{"~"},
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DefaultScanMode: config.ScanModeHome,
		AutoScanOnStart: true,
	})
	if m.mode != viewSearch {
		t.Fatalf("expected initial mode search, got %v", m.mode)
	}
}

func TestSlashFocusesSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:   "/tmp/test.db",
		Roots:    []string{"/"},
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewVolumes

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected search mode, got %v", next.mode)
	}
}

func TestCtrlAOpensTempRootInput(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		Roots:           []string{"~"},
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DefaultScanMode: config.ScanModeHome,
		AutoScanOnStart: true,
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	next := updated.(model)
	if !next.addRootActive {
		t.Fatalf("expected temporary root input active")
	}
}

func TestSearchInputAllowsTypingJKWhenInputFocused(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		Roots:           []string{"~"},
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DefaultScanMode: config.ScanModeHome,
		AutoScanOnStart: true,
	})
	m.searchRes = []db.Entry{{Name: "a", Path: "/tmp/a"}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	next := updated.(model)
	if next.searchInput.Value() != "j" {
		t.Fatalf("expected j to be typed in input, got %q", next.searchInput.Value())
	}
}

func TestSearchListFocusUsesJKForNavigation(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		Roots:           []string{"~"},
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DefaultScanMode: config.ScanModeHome,
		AutoScanOnStart: true,
	})
	m.searchRes = []db.Entry{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(model)
	if !next.searchListFocus {
		t.Fatalf("expected list focus after tab")
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	next = updated.(model)
	if next.searchCur != 1 {
		t.Fatalf("expected cursor to move to 1, got %d", next.searchCur)
	}
}
