package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"goeverything/internal/config"
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
