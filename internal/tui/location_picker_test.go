package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goeverything/internal/config"
	"goeverything/internal/db"
)

func TestLocationPickerRendersFocusedInputAndQuickRoots(t *testing.T) {
	m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	if m.mode != viewLocation || !m.locationInput.Focused() {
		t.Fatalf("expected focused location picker, mode=%v focused=%v", m.mode, m.locationInput.Focused())
	}
	out := m.View()
	for _, want := range []string{"SELECT LOCATION TO SCAN", "path>", "QUICK LOCATIONS", "Home (~)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in initial picker:\n%s", want, out)
		}
	}
}

func TestLocationPickerAutocompleteIsLevelBased(t *testing.T) {
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "Documents", "Nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	suggestions, err := locationSuggestions(filepath.Join(parent, "Doc"))
	if err != nil {
		t.Fatalf("suggestions: %v", err)
	}
	if len(suggestions) != 1 || suggestions[0] != filepath.Join(parent, "Documents") {
		t.Fatalf("unexpected level suggestions: %v", suggestions)
	}
}

func TestLocationPickerConfirmsRootAndTypedFolder(t *testing.T) {
	root := t.TempDir()
	m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.locationRoots = []string{root}

	updated, cmd := m.selectLocationRoot(0)
	if cmd == nil || updated.mode != viewStartup || !updated.busy {
		t.Fatalf("expected root confirmation to start scan, mode=%v busy=%v cmd=%v", updated.mode, updated.busy, cmd != nil)
	}
	if updated.activeScanRoot != filepath.Clean(root) {
		t.Fatalf("unexpected active root: %q", updated.activeScanRoot)
	}

	m = newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.locationInput.SetValue(root)
	updated, cmd = m.confirmLocation()
	if cmd == nil || updated.activeScanRoot != filepath.Clean(root) {
		t.Fatalf("expected typed folder confirmation, root=%q cmd=%v", updated.activeScanRoot, cmd != nil)
	}
}

func TestLocationPickerRejectsInvalidPathAndCancels(t *testing.T) {
	m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.locationInput.SetValue(filepath.Join(t.TempDir(), "missing"))
	updated, cmd := m.confirmLocation()
	if cmd != nil || updated.busy || updated.mode != viewLocation || updated.err == nil {
		t.Fatalf("expected invalid path error without scan: mode=%v busy=%v cmd=%v err=%v", updated.mode, updated.busy, cmd != nil, updated.err)
	}

	m = newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	updatedModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || updatedModel.(model).mode != viewLocation {
		t.Fatalf("expected Esc to quit from initial picker")
	}
}

func TestLocationPickerKeyboardAndMouseSelection(t *testing.T) {
	root := t.TempDir()
	m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.locationRoots = []string{root, filepath.Dir(root)}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.(model).locationRootCursor != 1 {
		t.Fatalf("expected down to select second quick root")
	}

	m = newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.locationRoots = []string{root}
	box := m.locationRootHitbox(0)
	updatedModel, cmd := m.Update(tea.MouseMsg(tea.MouseEvent{
		X:      box.x + 1,
		Y:      box.y,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}))
	if cmd == nil || !updatedModel.(model).busy {
		t.Fatalf("expected mouse root selection to start scan")
	}
}

func TestLocationInputBorderStaysThreeLines(t *testing.T) {
	for _, width := range []int{90, 52} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
			m.locationInput.SetValue(filepath.Join("/Users", "a", "Documents"))
			out := m.locationInputView(width)
			lines := strings.Split(out, "\n")
			if len(lines) != 3 {
				t.Fatalf("expected three input border lines, got %d:\n%s", len(lines), out)
			}
			for _, line := range lines {
				if lipgloss.Width(line) > max(20, min(100, width-4)) {
					t.Fatalf("input line wrapped at width %d: %q", width, line)
				}
			}
		})
	}
}

func TestSearchCommandScopesResultsToActiveRoot(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	store, err := db.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	rootA := filepath.Join(t.TempDir(), "a")
	rootB := filepath.Join(t.TempDir(), "b")
	entries := []db.Entry{
		db.NewEntryFromPath(rootA, filepath.Join(rootA, "same.txt"), 1, time.Now(), false),
		db.NewEntryFromPath(rootB, filepath.Join(rootB, "same.txt"), 1, time.Now(), false),
	}
	if err := store.UpsertBatch(ctx, entries); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	_ = store.Close()

	msg := searchCmd(ctx, dbPath, "same", rootA)()
	result, ok := msg.(searchDoneMsg)
	if !ok || result.err != nil {
		t.Fatalf("unexpected search message: %#v", msg)
	}
	if len(result.results) != 1 || result.results[0].Root != filepath.Clean(rootA) {
		t.Fatalf("expected only active root results, got %#v", result.results)
	}
}

func TestLastSearchIsPersistedAsSuggestion(t *testing.T) {
	var saved config.Config
	m := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db")})
	m.saveConfig = func(cfg config.Config) error {
		saved = cfg
		return nil
	}

	updated, _ := m.Update(debounceSearchMsg{seq: 0, query: "report"})
	next := updated.(model)
	if saved.LastSearch != "report" || next.cfg.LastSearch != "report" {
		t.Fatalf("expected last search persistence, saved=%q model=%q", saved.LastSearch, next.cfg.LastSearch)
	}

	loaded := newTestModel(t, config.Config{DBPath: filepath.Join(t.TempDir(), "index.db"), LastSearch: "report"})
	if !strings.Contains(loaded.searchInput.Placeholder, "report") {
		t.Fatalf("expected last search placeholder, got %q", loaded.searchInput.Placeholder)
	}
}
