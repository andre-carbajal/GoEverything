package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"goeverything/internal/config"
	"goeverything/internal/db"
)

func TestMenuEnterNavigatesToSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected search mode, got %v", next.mode)
	}
}

func TestNewModelStartsInMenu(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	if m.mode != viewMenu {
		t.Fatalf("expected initial mode menu, got %v", m.mode)
	}
}

func TestSlashFocusesSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	m.mode = viewMenu

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected search mode, got %v", next.mode)
	}
}

func TestSearchInputAllowsTypingJKWhenInputFocused(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	m.mode = viewSearch
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
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	m.mode = viewSearch
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

func TestCountDoneAlwaysStartsInitialScanFromDefaultPath(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})

	updated, cmd := m.Update(countDoneMsg{total: 7})
	next := updated.(model)

	if next.totalIndexed != 7 {
		t.Fatalf("expected indexed count updated, got %d", next.totalIndexed)
	}
	if !next.startupScanAttempted {
		t.Fatalf("expected startup scan to be attempted")
	}
	if !next.busy {
		t.Fatalf("expected model to be busy while startup scan runs")
	}
	if cmd == nil {
		t.Fatalf("expected startup scan command")
	}
}

func TestConfigViewRendersOnNarrowTerminal(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~/Projects/Very/Long/Path/For/Testing/Responsiveness",
		Excludes:        []string{".git", "Library/Caches/*"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewConfig
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 72, Height: 30})
	next := updated.(model)

	out := next.View()
	if !strings.Contains(out, "SETTINGS") {
		t.Fatalf("expected settings section in config view")
	}
	if strings.Contains(strings.ToLower(out), "auto scan on start") {
		t.Fatalf("did not expect deprecated auto scan toggle in config view")
	}
}

func TestMenuHeaderFallsBackToCompactWhenTerminalIsNarrow(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewMenu
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	next := updated.(model)

	out := next.View()
	if strings.Contains(out, "████████") {
		t.Fatalf("expected compact header on narrow terminal, got full ascii header")
	}
}
