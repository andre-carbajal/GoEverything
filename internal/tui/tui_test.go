package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goeverything/internal/config"
	"goeverything/internal/db"
	"goeverything/internal/scanner"
)

func newTestModel(t testing.TB, cfg config.Config) model {
	t.Helper()
	m := newModel(context.Background(), cfg)
	m.saveConfig = func(config.Config) error { return nil }
	return m
}

func TestSearchSettingsShortcutOpensConfig(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	next := updated.(model)
	if next.mode != viewConfig {
		t.Fatalf("expected config mode, got %v", next.mode)
	}
}

func TestNewModelStartsInLocationPicker(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	if m.mode != viewLocation {
		t.Fatalf("expected initial mode location picker, got %v", m.mode)
	}
	if !m.locationInput.Focused() {
		t.Fatalf("expected location input to be focused")
	}
	if m.status != "choose a location to scan" {
		t.Fatalf("expected location picker status, got %q", m.status)
	}
}

func TestFormatBytesIncludesDirectorySizes(t *testing.T) {
	t.Parallel()

	for size, want := range map[int64]string{
		0:           "0 B",
		512:         "512 B",
		1024:        "1.0 KB",
		1024 * 1024: "1.0 MB",
	} {
		if got := formatBytes(size); got != want {
			t.Fatalf("formatBytes(%d): want %q, got %q", size, want, got)
		}
	}

	values := searchEntryValues(db.Entry{
		Name:  "empty",
		Path:  "/tmp/empty",
		IsDir: true,
		Size:  0,
	})
	if values[0] != "dir" || values[3] != "0 B" {
		t.Fatalf("unexpected directory row: %#v", values)
	}
}

func TestUsageNavigationStaysWithinScannedRoot(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	root := "/tmp/root"
	m.activeScanRoot = root
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	next := updated.(model)
	if next.mode != viewUsage || next.usageRoot != root || next.usageBase != root {
		t.Fatalf("expected usage view at %q, got mode=%v root=%q base=%q", root, next.mode, next.usageRoot, next.usageBase)
	}

	next.usageBusy = false
	next.usageItems = []db.Entry{{Name: "child", Path: filepath.Join(root, "child"), IsDir: true}}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if next.usageRoot != filepath.Join(root, "child") {
		t.Fatalf("expected drill-down path, got %q", next.usageRoot)
	}

	next.usageBusy = false
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyLeft})
	next = updated.(model)
	if next.usageRoot != root {
		t.Fatalf("expected parent path, got %q", next.usageRoot)
	}
}

func TestUsageViewShowsFilesWithoutSelectionBackground(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	m.usageTotal = 100
	m.usageItems = []db.Entry{
		{Name: "folder", Path: "/tmp/folder", Size: 60, IsDir: true},
		{Name: "file.bin", Path: "/tmp/file.bin", Size: 40},
	}
	out := m.viewUsage(100, 20)
	if !strings.Contains(out, "[D]") || !strings.Contains(out, "[F]") {
		t.Fatalf("expected directory and file markers, got:\n%s", out)
	}
	if strings.Contains(out, "\x1b[48;") {
		t.Fatalf("selection should not paint a full-width background, got:\n%s", out)
	}
}

func TestUsageViewShowsBackRowOnlyBelowRoot(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	m.usageBase = "/tmp/root"
	m.usageRoot = filepath.Join(m.usageBase, "empty")
	if out := m.viewUsage(100, 20); !strings.Contains(out, ".. Volver") {
		t.Fatalf("expected back row below scanned root, got:\n%s", out)
	}
	m.usageRoot = m.usageBase
	if out := m.viewUsage(100, 20); strings.Contains(out, ".. Volver") {
		t.Fatalf("did not expect back row at scanned root, got:\n%s", out)
	}
}

func TestUsageNavigationSupportsBackRowAndListJumps(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	m.mode = viewUsage
	m.usageBase = "/tmp/root"
	m.usageRoot = filepath.Join(m.usageBase, "child")
	m.usageItems = make([]db.Entry, 30)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.usageRoot != m.usageBase {
		t.Fatalf("expected Enter on back row to return to %q, got %q", m.usageBase, next.usageRoot)
	}

	m.usageRoot = m.usageBase
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	next = updated.(model)
	if next.usageCur != 29 {
		t.Fatalf("expected End to select last item, got %d", next.usageCur)
	}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyHome})
	next = updated.(model)
	if next.usageCur != 0 {
		t.Fatalf("expected Home to select first item, got %d", next.usageCur)
	}
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	next = updated.(model)
	if next.usageCur <= 0 || next.usageCur >= len(next.usageItems) {
		t.Fatalf("expected PageDown to move within list, got %d", next.usageCur)
	}
}

func TestUsageViewScrollsFromItem24To25WithoutClippingBorder(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	m.mode = viewUsage
	m.usageBase = "/tmp/root"
	m.usageRoot = m.usageBase
	m.usageTotal = 465
	for i := 1; i <= 30; i++ {
		m.usageItems = append(m.usageItems, db.Entry{
			Name:  fmt.Sprintf("item-%02d", i),
			Path:  fmt.Sprintf("/tmp/root/item-%02d", i),
			Size:  int64(31 - i),
			IsDir: true,
		})
	}
	m.usageCur = 23

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.usageCur != 24 {
		t.Fatalf("expected cursor on item 25, got %d", next.usageCur+1)
	}

	out := next.viewUsage(120, 32)
	if !strings.Contains(out, "item-25") || strings.Contains(out, "item-01") {
		t.Fatalf("expected viewport to advance from item 24 to 25, got:\n%s", out)
	}
	if got := maxRenderedLineWidth(out); got > 120 {
		t.Fatalf("usage panel exceeded width 120: got %d\n%s", got, out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 32 || !strings.Contains(lines[len(lines)-1], "╰") || !strings.Contains(lines[len(lines)-1], "╯") {
		t.Fatalf("expected complete bottom border at height 32, got:\n%s", out)
	}
}

func TestUsageViewFitsNarrowWidth(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{DBPath: "/tmp/test.db", Theme: "tokyonight"})
	m.usageTotal = 100
	m.usageItems = []db.Entry{{Name: "very-long-directory-name", Size: 100, IsDir: true}}
	out := m.viewUsage(36, 14)
	if got := maxRenderedLineWidth(out); got > 36 {
		t.Fatalf("usage panel exceeded narrow width 36: got %d\n%s", got, out)
	}
}

func TestMouseReportsAreNotInsertedIntoSearchInput(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue(".docker")
	msg := tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("\x1b[<65;34;31M\x1b[<65;35;31M"),
	}

	updated, _ := m.Update(msg)
	next := updated.(model)
	if next.searchInput.Value() != ".docker" {
		t.Fatalf("mouse report polluted search input: %q", next.searchInput.Value())
	}
	if strings.Contains(next.searchInput.Value(), "65;34;31M") {
		t.Fatalf("mouse report remained in search input: %q", next.searchInput.Value())
	}
}

func TestSlashFromConfigDoesNotFocusSearch(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	next := updated.(model)
	if next.mode != viewConfig {
		t.Fatalf("expected config mode, got %v", next.mode)
	}
}

func TestSearchInputAllowsTypingJKWhenInputFocused(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{{Name: "a", Path: "/tmp/a"}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	next := updated.(model)
	if next.searchInput.Value() != "j" {
		t.Fatalf("expected j to be typed in input, got %q", next.searchInput.Value())
	}
}

func TestSearchInputAllowsTypingTestWithoutOpeningSettings(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	var updated tea.Model = m
	for _, r := range "test" {
		updated, _ = updated.(model).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected to stay in search mode, got %v", next.mode)
	}
	if next.searchInput.Value() != "test" {
		t.Fatalf("expected test to be typed in input, got %q", next.searchInput.Value())
	}
}

func TestSearchInputAllowsTypingDWhenInputFocused(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{{Name: "delete-me.txt", Path: "/tmp/delete-me.txt"}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	next := updated.(model)
	if next.searchInput.Value() != "d" {
		t.Fatalf("expected d to be typed in input, got %q", next.searchInput.Value())
	}
	if next.modal != noModal {
		t.Fatalf("did not expect delete modal with input focused, got %v", next.modal)
	}
}

func TestSearchInputAllowsTypingSlash(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	next := updated.(model)
	if next.searchInput.Value() != "/" {
		t.Fatalf("expected slash to be typed in input, got %q", next.searchInput.Value())
	}
}

func TestSearchTabDoesNotSwitchFocus(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue("a")
	m.searchSeq = 7
	m.searchRes = []db.Entry{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(model)
	if next.searchInput.Value() != "a" {
		t.Fatalf("expected tab to leave input value unchanged, got %q", next.searchInput.Value())
	}
	if next.searchCur != 0 || next.searchTable.Cursor() > 0 {
		t.Fatalf("expected tab to leave selection unchanged, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}
	if next.searchSeq != 7 {
		t.Fatalf("expected tab not to trigger search debounce, got seq %d", next.searchSeq)
	}
}

func TestSearchInputArrowDownNavigatesResults(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue("a")
	m.searchRes = []db.Entry{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.searchCur != 1 || next.searchTable.Cursor() != 1 {
		t.Fatalf("expected cursor to move to 1, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}
	if next.searchInput.Value() != "a" {
		t.Fatalf("expected input value to stay unchanged, got %q", next.searchInput.Value())
	}
}

func TestSearchInputArrowUpNavigatesResults(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue("b")
	m.searchRes = []db.Entry{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
	}
	m.searchCur = 1
	m = m.syncSearchTableRows()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	next := updated.(model)
	if next.searchCur != 0 || next.searchTable.Cursor() > 0 {
		t.Fatalf("expected cursor to move to 0, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}
	if next.searchInput.Value() != "b" {
		t.Fatalf("expected input value to stay unchanged, got %q", next.searchInput.Value())
	}
}

func TestSearchInputArrowDownWithoutResultsKeepsInput(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue("missing")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	if next.searchInput.Value() != "missing" {
		t.Fatalf("expected input value to stay unchanged, got %q", next.searchInput.Value())
	}
	if next.searchCur != 0 || next.searchTable.Cursor() > 0 {
		t.Fatalf("expected cursor to stay at 0, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}
}

func TestSearchInputEnterOpensArrowSelectedResult(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "a", Path: "/tmp/a"},
		{Name: "b", Path: "/tmp/b"},
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	next := updated.(model)
	updated, cmd := next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)

	if next.searchCur != 1 {
		t.Fatalf("expected selected cursor to stay at 1, got %d", next.searchCur)
	}
	if cmd == nil {
		t.Fatalf("expected enter to open selected result")
	}
}

func TestSearchResultSelectionRendersFullRowWidth(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "one.txt", Path: "/tmp/one.txt", Size: 1024},
		{Name: "two.txt", Path: "/tmp/two.txt", Size: 2048},
	}
	m = m.syncSearchTableRows()
	m.searchTable.SetCursor(1)

	out := m.renderSearchResults()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "two.txt") {
			if got, want := lipgloss.Width(line), m.searchTable.Width(); got != want {
				t.Fatalf("expected selected row width %d, got %d in line %q", want, got, line)
			}
			return
		}
	}
	t.Fatalf("selected row not found in rendered results:\n%s", out)
}

func maxRenderedLineWidth(block string) int {
	maxWidth := 0
	for _, line := range strings.Split(block, "\n") {
		maxWidth = max(maxWidth, lipgloss.Width(line))
	}
	return maxWidth
}

func TestSearchCtrlDOpensDeleteConfirmationAndCancelKeepsResults(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{{Name: "delete-me.txt", Path: "/tmp/delete-me.txt"}}
	m = m.syncSearchTableRows()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	next := updated.(model)

	if next.modal != deleteConfirmModal || !next.hasDeleteTarget {
		t.Fatalf("expected delete confirmation modal, modal=%v hasTarget=%v", next.modal, next.hasDeleteTarget)
	}
	out := next.View()
	if !strings.Contains(out, "DELETE RESULT") || !strings.Contains(out, "Trash") {
		t.Fatalf("expected delete confirmation with trash mode, got:\n%s", out)
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next = updated.(model)
	if next.modal != noModal {
		t.Fatalf("expected modal to close after esc, got %v", next.modal)
	}
	if len(next.searchRes) != 1 {
		t.Fatalf("expected cancel to keep result, got %d results", len(next.searchRes))
	}
}

func TestSearchDeleteKeyOpensDeleteConfirmation(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{{Name: "delete-me.txt", Path: "/tmp/delete-me.txt"}}
	m = m.syncSearchTableRows()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	next := updated.(model)
	if next.modal != deleteConfirmModal || !next.hasDeleteTarget {
		t.Fatalf("expected delete key to open confirmation, modal=%v hasTarget=%v", next.modal, next.hasDeleteTarget)
	}
}

func TestDeleteConfirmationEnterStartsDeleteCommand(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModePermanent,
	})
	m.mode = viewSearch
	m.modal = deleteConfirmModal
	m.hasDeleteTarget = true
	m.deleteIndex = 0
	m.deleteTarget = db.Entry{Name: "delete-me.txt", Path: "/tmp/delete-me.txt"}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.modal != noModal || next.hasDeleteTarget {
		t.Fatalf("expected confirm to close modal and clear target, modal=%v hasTarget=%v", next.modal, next.hasDeleteTarget)
	}
	if cmd == nil {
		t.Fatalf("expected delete command after confirm")
	}
}

func TestDeleteResultSuccessRemovesResultAndClampsCursor(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModePermanent,
	})
	m.mode = viewSearch
	deleted := db.Entry{Name: "folder", Path: "/tmp/folder", IsDir: true}
	m.searchRes = []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt"},
		deleted,
		{Name: "child.txt", Path: "/tmp/folder/child.txt"},
	}
	m.searchCur = 2
	m = m.syncSearchTableRows()

	updated, _ := m.Update(deleteResultDoneMsg{entry: deleted, total: 1})
	next := updated.(model)

	if len(next.searchRes) != 1 || next.searchRes[0].Path != "/tmp/a.txt" {
		t.Fatalf("expected deleted folder and children removed, got %#v", next.searchRes)
	}
	if next.searchCur != 0 || next.searchTable.Cursor() != 0 {
		t.Fatalf("expected cursor clamped to 0, got searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}
	if next.totalIndexed != 1 {
		t.Fatalf("expected total indexed updated to 1, got %d", next.totalIndexed)
	}
}

func TestCountDoneDoesNotStartScanAutomatically(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})

	updated, cmd := m.Update(countDoneMsg{total: 7})
	next := updated.(model)

	if next.totalIndexed != 7 {
		t.Fatalf("expected indexed count updated, got %d", next.totalIndexed)
	}
	if next.startupScanAttempted || next.busy {
		t.Fatalf("count refresh must not trigger a scan")
	}
	if cmd != nil {
		t.Fatalf("did not expect a startup scan command")
	}
}

func TestStartupViewShowsProgressByDefault(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true
	m.activeScanLabel = "initial-scan"

	out := m.View()
	for _, want := range []string{"Scanning before search opens", "PROGRESS", "scanned: 0", "indexed: 0", "skipped: 0", "waiting for first path"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in startup progress view, got:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"GoEverything", "status:", "keys:", "Scan location", "Search will open automatically", "Press Space"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("did not expect %q in startup progress view, got:\n%s", hidden, out)
		}
	}
}

func TestStartupSpaceDoesNotHideProgress(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true
	m.scanProgress = scanner.Progress{
		Scanned:        10,
		Indexed:        8,
		Skipped:        2,
		Elapsed:        2 * time.Second,
		FilesPerSecond: 5,
		CurrentPath:    "/tmp/current-file.txt",
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	next := updated.(model)
	out := next.View()
	for _, want := range []string{"PROGRESS", "scanned: 10", "indexed: 8", "skipped: 2", "current: /tmp/current-file.txt"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in progress view, got:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"GoEverything", "status:", "keys:"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("did not expect background chrome %q in progress view, got:\n%s", hidden, out)
		}
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeySpace})
	next = updated.(model)
	out = next.View()
	if !strings.Contains(out, "PROGRESS") || !strings.Contains(out, "scanned: 10") {
		t.Fatalf("expected space to leave progress visible, got:\n%s", out)
	}
}

func TestStartupCtrlXCancelsAndOnlyCtrlQQuits(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true
	m.scanCancel = cancel

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("expected ctrl+x to only cancel current scan")
	}
	if next.status != "stopping scan..." {
		t.Fatalf("expected stopping scan status, got %q", next.status)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatalf("expected ctrl+x to cancel scan context")
	}

	_, cmd = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Fatalf("did not expect q to quit during startup scan")
	}

	_, cmd = next.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd == nil {
		t.Fatalf("expected ctrl+q to quit during startup scan")
	}
}

func TestScanProgressTickUpdatesStartupProgress(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true
	m.scanSession = 9
	m.scanProgressSource.reset(9)
	m.scanProgressSource.update(9, scanner.Progress{
		Scanned:        20,
		Indexed:        16,
		Skipped:        4,
		Elapsed:        4 * time.Second,
		FilesPerSecond: 5,
		CurrentPath:    "/tmp/live.txt",
	})

	updated, cmd := m.Update(scanProgressTickMsg{session: 9})
	next := updated.(model)
	if next.mode != viewStartup {
		t.Fatalf("expected progress tick to keep startup mode, got %v", next.mode)
	}
	if next.scanProgress.Scanned != 20 || next.scanProgress.CurrentPath != "/tmp/live.txt" {
		t.Fatalf("expected progress snapshot to update, got %#v", next.scanProgress)
	}
	if cmd == nil {
		t.Fatalf("expected another progress tick command while busy")
	}
}

func TestInitialScanSuccessOpensSearch(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true

	updated, cmd := m.Update(scanDoneMsg{label: "initial-scan"})
	next := updated.(model)

	if next.mode != viewSearch {
		t.Fatalf("expected search mode after successful startup scan, got %v", next.mode)
	}
	if next.busy {
		t.Fatalf("expected startup scan to stop being busy")
	}
	if cmd == nil {
		t.Fatalf("expected count refresh command")
	}
}

func TestInitialScanFailureOpensConfig(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true

	updated, cmd := m.Update(scanDoneMsg{label: "initial-scan", err: errors.New("permission denied")})
	next := updated.(model)

	if next.mode != viewLocation {
		t.Fatalf("expected location picker after failed startup scan, got %v", next.mode)
	}
	if next.err == nil || !strings.Contains(next.err.Error(), "permission denied") {
		t.Fatalf("expected startup scan error to be preserved, got %v", next.err)
	}
	if cmd == nil {
		t.Fatalf("expected count refresh command")
	}
}

func TestManualScanSuccessReturnsToSearch(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewStartup
	m.busy = true
	m.activeScanLabel = "manual-scan"

	updated, cmd := m.Update(scanDoneMsg{label: "manual-scan"})
	next := updated.(model)

	if next.mode != viewSearch {
		t.Fatalf("expected search mode after successful manual scan, got %v", next.mode)
	}
	if next.busy {
		t.Fatalf("expected manual scan to stop being busy")
	}
	if cmd == nil {
		t.Fatalf("expected count refresh command")
	}
}

func TestCtrlGOpensLocationPicker(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	next := updated.(model)

	if next.mode != viewLocation {
		t.Fatalf("expected ctrl+g to open location picker, got %v", next.mode)
	}
	if next.busy {
		t.Fatalf("ctrl+g must wait for location confirmation")
	}
	if next.locationScanLabel != "manual-scan" {
		t.Fatalf("expected manual scan label, got %q", next.locationScanLabel)
	}
	if cmd != nil {
		t.Fatalf("did not expect a scan command before confirmation")
	}
	out := next.View()
	if !strings.Contains(out, "SELECT LOCATION TO SCAN") {
		t.Fatalf("expected location picker, got:\n%s", out)
	}
}

func TestEscFromConfigReturnsToSearch(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	next := updated.(model)

	if next.mode != viewSearch {
		t.Fatalf("expected search mode after esc from config, got %v", next.mode)
	}
}

func mouseEventIn(box hitbox, action tea.MouseAction, button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg(tea.MouseEvent{
		X:      box.x + max(0, box.w/2),
		Y:      box.y + max(0, box.h/2),
		Action: action,
		Button: button,
	})
}

func mouseEventAt(x, y int, action tea.MouseAction, button tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg(tea.MouseEvent{
		X:      x,
		Y:      y,
		Action: action,
		Button: button,
	})
}

func renderedLineForSearchRow(t *testing.T, m model, index int) int {
	t.Helper()
	needle := fmt.Sprintf("row-%02d", index)
	for y, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, needle) {
			return y
		}
	}
	t.Fatalf("could not find rendered row %q in view:\n%s", needle, m.View())
	return 0
}

func renderedHelpLine(t *testing.T, m model) int {
	t.Helper()
	for y, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "keys:") {
			return y
		}
	}
	t.Fatalf("could not find help line in view:\n%s", m.View())
	return 0
}

func modelWithManySearchRows(t testing.TB) model {
	t.Helper()
	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewSearch
	m.searchRes = make([]db.Entry, 24)
	for i := range m.searchRes {
		m.searchRes[i] = db.Entry{
			Name: fmt.Sprintf("row-%02d", i),
			Path: fmt.Sprintf("/tmp/search-row-%02d", i),
			Size: int64(i + 1),
		}
	}
	m.searchCur = 10
	m = m.syncSearchTableRows()
	return m
}

func TestMouseHoverTracksInteractiveTargetsWithoutActivating(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git", "node_modules"},
		Theme:    "tokyonight",
	}

	m := newTestModel(t, cfg)
	m.mode = viewConfig
	m.cfgCursor = 0
	updated, _ := m.Update(mouseEventIn(m.configRowHitbox(1), tea.MouseActionMotion, tea.MouseButtonNone))
	next := updated.(model)
	if next.hoveredMouse.kind != mouseTargetConfigRow || next.hoveredMouse.index != 1 {
		t.Fatalf("expected config row hover, got %#v", next.hoveredMouse)
	}
	if next.cfgCursor != 0 || next.modal != noModal {
		t.Fatalf("hover should not activate config, cursor=%d modal=%v", next.cfgCursor, next.modal)
	}

	m = newTestModel(t, cfg)
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt"},
		{Name: "b.txt", Path: "/tmp/b.txt"},
	}
	m = m.syncSearchTableRows()
	updated, _ = m.Update(mouseEventIn(m.searchResultHitbox(1), tea.MouseActionMotion, tea.MouseButtonNone))
	next = updated.(model)
	if next.hoveredMouse.kind != mouseTargetSearchResult || next.hoveredMouse.index != 1 {
		t.Fatalf("expected search result hover, got %#v", next.hoveredMouse)
	}
	if next.searchCur != 0 {
		t.Fatalf("hover should not select result, cursor=%d", next.searchCur)
	}
}

func TestSearchResultHitboxesMatchRenderedRows(t *testing.T) {
	t.Parallel()

	m := modelWithManySearchRows(t)
	layout := m.searchMouseLayout()
	if layout.visibleStart == layout.visibleEnd {
		t.Fatalf("expected visible search rows")
	}

	for i := layout.visibleStart; i < layout.visibleEnd; i++ {
		renderedY := renderedLineForSearchRow(t, m, i)
		box := m.searchResultHitbox(i)
		if box.y != renderedY {
			t.Fatalf("expected row %d hitbox y to match rendered line %d, got %d", i, renderedY, box.y)
		}
		target := m.resolveMouseTarget(layout.resultX+8, renderedY)
		if target.kind != mouseTargetSearchResult || target.index != i {
			t.Fatalf("expected rendered row %d to resolve to itself, got %#v", i, target)
		}
	}
}

func TestSearchResultMouseActionsUseRenderedRowCoordinates(t *testing.T) {
	t.Parallel()

	m := modelWithManySearchRows(t)
	layout := m.searchMouseLayout()
	row := layout.visibleStart + 3
	x := layout.resultX + 8
	y := renderedLineForSearchRow(t, m, row)

	updated, cmd := m.Update(mouseEventAt(x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("did not expect open command on first rendered-row click")
	}
	if next.searchCur != row || next.searchTable.Cursor() != row {
		t.Fatalf("expected rendered-row click to select row %d, searchCur=%d tableCursor=%d", row, next.searchCur, next.searchTable.Cursor())
	}

	updated, cmd = next.Update(mouseEventAt(x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	next = updated.(model)
	if cmd == nil {
		t.Fatalf("expected rendered-row double-click to return open command")
	}

	rightClickRow := layout.visibleStart + 1
	y = renderedLineForSearchRow(t, next, rightClickRow)
	updated, _ = next.Update(mouseEventAt(x, y, tea.MouseActionPress, tea.MouseButtonRight))
	next = updated.(model)
	if next.modal != deleteConfirmModal || next.deleteIndex != rightClickRow || next.deleteTarget.Name != fmt.Sprintf("row-%02d", rightClickRow) {
		t.Fatalf("expected right-click on rendered row %d to target that row, modal=%v index=%d target=%q", rightClickRow, next.modal, next.deleteIndex, next.deleteTarget.Name)
	}
}

func TestMouseLeftClickActivatesConfigRows(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig
	updated, _ := m.Update(mouseEventIn(m.configRowHitbox(0), tea.MouseActionPress, tea.MouseButtonLeft))
	next := updated.(model)
	if next.modal != themeModal || next.cfgCursor != 0 {
		t.Fatalf("expected theme row click to open theme modal, modal=%v cursor=%d", next.modal, next.cfgCursor)
	}
}

func TestSearchSettingsMouseClickOpensConfig(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(model)
	box := m.settingsButtonHitbox()

	updated, _ = m.Update(tea.MouseMsg(tea.MouseEvent{
		X:      box.x + box.w/2,
		Y:      box.y + box.h/2,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	}))
	next := updated.(model)

	if next.mode != viewConfig {
		t.Fatalf("expected config mode after settings click, got %v", next.mode)
	}
}

func TestSearchResultMouseClickDoubleClickAndRightClick(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt"},
		{Name: "b.txt", Path: "/tmp/b.txt"},
	}
	m = m.syncSearchTableRows()

	updated, cmd := m.Update(mouseEventIn(m.searchResultHitbox(1), tea.MouseActionPress, tea.MouseButtonLeft))
	next := updated.(model)
	if cmd != nil {
		t.Fatalf("did not expect open command on first click")
	}
	if next.searchCur != 1 || next.searchTable.Cursor() != 1 {
		t.Fatalf("expected first click to select row 1, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}

	updated, cmd = next.Update(mouseEventIn(next.searchResultHitbox(1), tea.MouseActionPress, tea.MouseButtonLeft))
	next = updated.(model)
	if cmd == nil {
		t.Fatalf("expected double-click to return open command")
	}

	updated, _ = next.Update(mouseEventIn(next.searchResultHitbox(0), tea.MouseActionPress, tea.MouseButtonRight))
	next = updated.(model)
	if next.modal != deleteConfirmModal || !next.hasDeleteTarget || next.deleteIndex != 0 {
		t.Fatalf("expected right-click to open delete confirmation for row 0, modal=%v hasTarget=%v deleteIndex=%d", next.modal, next.hasDeleteTarget, next.deleteIndex)
	}
}

func TestSearchResultRightReleaseOpensDeleteConfirmation(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt"},
		{Name: "b.txt", Path: "/tmp/b.txt"},
	}
	m = m.syncSearchTableRows()

	updated, _ := m.Update(mouseEventIn(m.searchResultHitbox(1), tea.MouseActionRelease, tea.MouseButtonRight))
	next := updated.(model)
	if next.modal != deleteConfirmModal || !next.hasDeleteTarget || next.deleteIndex != 1 {
		t.Fatalf("expected right release to open delete confirmation for row 1, modal=%v hasTarget=%v deleteIndex=%d", next.modal, next.hasDeleteTarget, next.deleteIndex)
	}
}

func TestMouseWheelMovesSearchConfigAndModalCursors(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git", "node_modules"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{
		{Name: "a.txt", Path: "/tmp/a.txt"},
		{Name: "b.txt", Path: "/tmp/b.txt"},
		{Name: "c.txt", Path: "/tmp/c.txt"},
	}
	m = m.syncSearchTableRows()

	updated, _ := m.Update(mouseEventIn(m.searchResultHitbox(0), tea.MouseActionPress, tea.MouseButtonWheelDown))
	next := updated.(model)
	if next.searchCur != 1 || next.searchTable.Cursor() != 1 {
		t.Fatalf("expected wheel down to move search cursor to 1, searchCur=%d tableCursor=%d", next.searchCur, next.searchTable.Cursor())
	}

	m = newTestModel(t, m.cfg)
	m.mode = viewConfig
	updated, _ = m.Update(mouseEventIn(m.configRowHitbox(0), tea.MouseActionPress, tea.MouseButtonWheelDown))
	next = updated.(model)
	if next.cfgCursor != 1 {
		t.Fatalf("expected wheel down to move config cursor to 1, got %d", next.cfgCursor)
	}

	m = newTestModel(t, m.cfg)
	m.mode = viewConfig
	m.modal = themeModal
	m.cfgThemeCursor = 0
	updated, _ = m.Update(mouseEventIn(m.themeOptionHitbox(0), tea.MouseActionPress, tea.MouseButtonWheelDown))
	next = updated.(model)
	if next.cfgThemeCursor != 1 {
		t.Fatalf("expected wheel down to move theme cursor to 1, got %d", next.cfgThemeCursor)
	}
}

func TestConfigViewRendersOnNarrowTerminal(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git", "Library/Caches/*"},
		Theme:    "tokyonight",
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
	if strings.Contains(out, "status:") {
		t.Fatalf("did not expect status footer in config view, got:\n%s", out)
	}
}

func TestTopBarAndConfigViewUseFullWideTerminalWidth(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git", "node_modules"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next := updated.(model)
	bodyW, _ := next.bodySize()

	topW := maxRenderedLineWidth(next.renderTopBar())
	if topW <= 120 || topW < bodyW {
		t.Fatalf("expected top bar to use wide terminal width, got %d body width %d", topW, bodyW)
	}

	configW := maxRenderedLineWidth(next.viewConfig(bodyW, 30))
	searchW := maxRenderedLineWidth(next.viewSearch(bodyW, 30))
	if configW <= 120 || configW != searchW {
		t.Fatalf("expected config width to match search width on wide terminal, config=%d search=%d", configW, searchW)
	}
}

func TestConfigHitboxesUseFullWideTerminalLayout(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git", "node_modules"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next := updated.(model)
	bodyW, _ := next.bodySize()
	layout := configLayoutForWidth(bodyW)
	if layout.outerWidth <= 120 {
		t.Fatalf("expected wide config layout to exceed old 120-column cap, got %d", layout.outerWidth)
	}

	row := next.configRowHitbox(0)
	if got, want := row.w, max(28, layout.leftW-4); got != want {
		t.Fatalf("expected config row hitbox width %d, got %d", want, got)
	}

	exclude := next.configExcludeHitbox(0)
	if got, want := exclude.x, next.contentStartX()+layout.leftW+layout.gap+1; got != want {
		t.Fatalf("expected exclude hitbox x %d, got %d", want, got)
	}
	if got, want := exclude.w, max(22, layout.rightW-4); got != want {
		t.Fatalf("expected exclude hitbox width %d, got %d", want, got)
	}
}

func TestExcludeInputModalDoesNotOverflow(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.modal = excludeInputModal
	m.cfgInputActive = true
	m.cfgInput.Placeholder = "Add exclude (example: .git or Library/Caches/*)"
	m.cfgInput.Prompt = "exclude> "
	m.cfgInput.SetValue("")

	width := 90
	out := m.renderExcludeInputModal(width)
	maxWidth := m.modalWidth(width) + 2
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if got := lipgloss.Width(line); got > maxWidth {
			t.Fatalf("modal line overflowed: width=%d max=%d line=%q\n%s", got, maxWidth, line, out)
		}
	}
	if len(lines) > 10 {
		t.Fatalf("modal input appears wrapped; got %d lines:\n%s", len(lines), out)
	}
}

func TestExcludeInputModalFieldAlwaysHasThreeLines(t *testing.T) {
	for _, width := range []int{90, 52} {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			m := newTestModel(t, config.Config{
				DBPath:   "/tmp/test.db",
				Excludes: []string{".git"},
				Theme:    "tokyonight",
			})
			m.cfgInput.Prompt = "exclude> "
			m.cfgInput.SetValue("Library/Caches/very-long-pattern-name/*")
			field := m.renderModalInput(width)
			lines := strings.Split(field, "\n")
			if len(lines) != 3 {
				t.Fatalf("expected top/content/bottom only, got %d lines:\n%s", len(lines), field)
			}
		})
	}
}

func TestConfigViewShowsAndTogglesDeleteMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.mode = viewConfig

	out := m.View()
	if !strings.Contains(out, "delete mode") || !strings.Contains(out, "Trash") {
		t.Fatalf("expected delete mode setting rendered, got:\n%s", out)
	}

	m.cfgCursor = 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.cfg.DeleteMode != config.DeleteModePermanent {
		t.Fatalf("expected delete mode to toggle to permanent, got %q", next.cfg.DeleteMode)
	}
}

func TestConfigActionsUseInjectedSaveConfig(t *testing.T) {
	t.Parallel()

	var saved []config.Config
	m := newTestModel(t, config.Config{
		DBPath:     "/tmp/test.db",
		Excludes:   []string{".git"},
		Theme:      "tokyonight",
		DeleteMode: config.DeleteModeTrash,
	})
	m.saveConfig = func(cfg config.Config) error {
		saved = append(saved, cfg)
		return nil
	}
	m.mode = viewConfig
	m.cfgCursor = 1

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.err != nil {
		t.Fatalf("did not expect save error: %v", next.err)
	}
	if len(saved) != 1 {
		t.Fatalf("expected one injected save call, got %d", len(saved))
	}
	if saved[0].DeleteMode != config.DeleteModePermanent {
		t.Fatalf("expected injected save to receive permanent delete mode, got %q", saved[0].DeleteMode)
	}
}

func TestExcludePatternEnterPersistsAndEscCancels(t *testing.T) {
	var saved []config.Config
	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.saveConfig = func(cfg config.Config) error {
		saved = append(saved, cfg)
		return nil
	}
	m.mode = viewConfig
	m.cfgCursor = 0
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next := updated.(model)
	if next.modal != excludeInputModal {
		t.Fatalf("expected add exclude modal, got %v", next.modal)
	}
	next.cfgInput.SetValue("Library/Caches/*")
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next = updated.(model)
	if !slices.Contains(next.cfg.Excludes, "Library/Caches/*") || len(saved) != 1 {
		t.Fatalf("expected pattern to persist, excludes=%v saves=%d", next.cfg.Excludes, len(saved))
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next = updated.(model)
	next.cfgInput.SetValue("should-not-save")
	updated, _ = next.Update(tea.KeyEsc)
	next = updated.(model)
	if slices.Contains(next.cfg.Excludes, "should-not-save") || len(saved) != 1 {
		t.Fatalf("expected Esc to cancel without persistence, excludes=%v saves=%d", next.cfg.Excludes, len(saved))
	}
}

func TestTopBarHidesIdleStatusWhenNotBusy(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.busy = false

	out := m.renderTopBar()
	if strings.Contains(out, "idle") {
		t.Fatalf("did not expect idle status in top bar, got:\n%s", out)
	}

	m.busy = true
	out = m.renderTopBar()
	if !strings.Contains(out, "scanning") {
		t.Fatalf("expected scanning status while busy, got:\n%s", out)
	}
}

func TestWindowSizeMsgStoresWidthAndHeight(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	next := updated.(model)
	if next.width != 100 || next.height != 40 {
		t.Fatalf("expected size 100x40, got %dx%d", next.width, next.height)
	}
}

func TestSearchViewRendersTableResults(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.searchInput.SetValue("report")

	updated, _ := m.Update(searchDoneMsg{
		query: "report",
		results: []db.Entry{{
			Name: "report.pdf",
			Path: "/tmp/docs/report.pdf",
			Size: 2048,
		}},
	})
	next := updated.(model)
	out := next.View()
	if !strings.Contains(out, "RESULTS") || !strings.Contains(out, "report.pdf") || !strings.Contains(out, "Directory") {
		t.Fatalf("expected table results in search view, got:\n%s", out)
	}
	if strings.Contains(out, "status:") {
		t.Fatalf("did not expect status footer in search view, got:\n%s", out)
	}
	if strings.Contains(out, "path-filter") || strings.Contains(strings.ToLower(out), "ctrl+p") {
		t.Fatalf("did not expect path filter UI/help in search view, got:\n%s", out)
	}
	lower := strings.ToLower(out)
	for _, want := range []string{"ctrl+d/delete/right-click delete", "enter/double-click open", "ctrl+q quit"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("expected %q in search help, got:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"ctrl+s settings", "delete remove"} {
		if strings.Contains(lower, hidden) {
			t.Fatalf("did not expect %q in search help, got:\n%s", hidden, out)
		}
	}
}

func TestSearchViewEmptyResultsUsesStableResultsArea(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 36})
	m = updated.(model)

	out := m.renderEmptySearchResults()
	if !strings.Contains(out, "No results") {
		t.Fatalf("expected empty results message, got:\n%s", out)
	}
	if got, want := lipgloss.Height(out), m.searchTable.Height()+1; got != want {
		t.Fatalf("expected empty results block height %d, got %d", want, got)
	}
	lines := strings.Split(out, "\n")
	if strings.Contains(lines[0], "No results") {
		t.Fatalf("expected empty message to be vertically centered, got:\n%s", out)
	}
}

func TestSearchHelpLineStaysFixedWithFewOrManyResults(t *testing.T) {
	t.Parallel()

	few := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	few.mode = viewSearch
	few.searchRes = []db.Entry{
		{Name: "Terraria", Path: `C:\Users\andre\Documents\My Games`, IsDir: true},
		{Name: "Terraria.url", Path: `C:\Users\andre\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Steam`, Size: 200},
	}
	updated, _ := few.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	few = updated.(model).syncSearchTableRows()

	many := newTestModel(t, few.cfg)
	many.mode = viewSearch
	for i := 0; i < 40; i++ {
		many.searchRes = append(many.searchRes, db.Entry{
			Name: fmt.Sprintf("row-%02d.md", i),
			Path: fmt.Sprintf("/tmp/specs/APP-%04d", i),
			Size: int64(i + 1),
		})
	}
	updated, _ = many.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	many = updated.(model).syncSearchTableRows()

	fewHelp := renderedHelpLine(t, few)
	manyHelp := renderedHelpLine(t, many)
	if fewHelp != manyHelp {
		t.Fatalf("expected help line to stay fixed, few=%d many=%d\nfew:\n%s\nmany:\n%s", fewHelp, manyHelp, few.View(), many.View())
	}
	lines := strings.Split(few.View(), "\n")
	if want := len(lines) - 2; fewHelp != want {
		t.Fatalf("expected help line immediately above bottom border at line %d, got %d of %d\n%s", want, fewHelp, len(lines), few.View())
	}
}

func TestSearchCtrlPNoLongerTogglesPathFilter(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	next := updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected to stay in search mode, got %v", next.mode)
	}
	out := next.View()
	if strings.Contains(out, "path-filter") || strings.Contains(strings.ToLower(out), "ctrl+p") {
		t.Fatalf("did not expect path filter UI/help after ctrl+p, got:\n%s", out)
	}
}

func TestOpenCommandRevealsPathByPlatform(t *testing.T) {
	t.Parallel()

	path := "/tmp/report.txt"
	if runtime.GOOS == "windows" {
		path = `C:\Users\andre\Documents\report.txt`
	}

	cmd := openCommand(path, true)
	switch runtime.GOOS {
	case "windows":
		if len(cmd.Args) != 2 || cmd.Args[0] != "explorer.exe" || cmd.Args[1] != "/select,"+path {
			t.Fatalf("expected explorer reveal command, got %#v", cmd.Args)
		}
	case "darwin":
		if len(cmd.Args) != 3 || cmd.Args[0] != "open" || cmd.Args[1] != "-R" || cmd.Args[2] != path {
			t.Fatalf("expected macOS reveal command, got %#v", cmd.Args)
		}
	default:
		if len(cmd.Args) != 2 || cmd.Args[0] != "xdg-open" || cmd.Args[1] != "/tmp" {
			t.Fatalf("expected xdg-open parent command, got %#v", cmd.Args)
		}
	}
}

func TestOnlyCtrlQQuitsFromSearch(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	next := updated.(model)
	if next.mode != viewSearch || next.searchInput.Value() != "q" {
		t.Fatalf("expected q to be typed in search, mode=%v value=%q", next.mode, next.searchInput.Value())
	}

	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	next = updated.(model)
	if next.mode != viewSearch {
		t.Fatalf("expected ctrl+c to leave mode unchanged, got %v", next.mode)
	}

	_, cmd := next.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	if cmd == nil {
		t.Fatalf("expected ctrl+q to quit")
	}
}

func TestErrorRendersWithoutStatusFooter(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewSearch
	m.err = errors.New("boom")

	out := m.View()
	if !strings.Contains(out, "error: boom") {
		t.Fatalf("expected error line to render, got:\n%s", out)
	}
	if strings.Contains(out, "status:") {
		t.Fatalf("did not expect status footer with error, got:\n%s", out)
	}
}

func TestConfigModalReplacesCentralContent(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	m.mode = viewConfig
	m.modal = themeModal

	out := m.View()
	if !strings.Contains(out, "SELECT THEME") {
		t.Fatalf("expected centered theme modal, got:\n%s", out)
	}
	if strings.Contains(out, "SETTINGS") {
		t.Fatalf("expected modal to replace config content, got:\n%s", out)
	}
}

func TestFullScreenFrameRendersBorder(t *testing.T) {
	t.Parallel()

	m := newTestModel(t, config.Config{
		DBPath:   "/tmp/test.db",
		Excludes: []string{".git"},
		Theme:    "tokyonight",
	})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	next := updated.(model)

	out := next.View()
	if !strings.Contains(out, "╭") || !strings.Contains(out, "╯") {
		t.Fatalf("expected full-screen frame border, got:\n%s", out)
	}
	if strings.Contains(out, "████████") {
		t.Fatalf("expected compact header on narrow terminal, got full ascii header")
	}
}
