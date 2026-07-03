package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goeverything/internal/config"
	"goeverything/internal/db"
	"goeverything/internal/scanner"
)

func TestSearchSettingsShortcutOpensConfig(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	m.mode = viewSearch

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	next := updated.(model)
	if next.mode != viewConfig {
		t.Fatalf("expected config mode, got %v", next.mode)
	}
}

func TestNewModelStartsInStartupView(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
	})
	if m.mode != viewStartup {
		t.Fatalf("expected initial mode startup, got %v", m.mode)
	}
	if m.status != "preparing initial scan" {
		t.Fatalf("expected startup scan status, got %q", m.status)
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

func TestSearchInputAllowsTypingTestWithoutOpeningSettings(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: true,
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

func TestSearchResultSelectionRendersFullRowWidth(t *testing.T) {
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

func TestSearchListDOpensDeleteConfirmationAndCancelKeepsResults(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModeTrash,
		AutoScanOnStart: true,
	})
	m.mode = viewSearch
	m.searchRes = []db.Entry{{Name: "delete-me.txt", Path: "/tmp/delete-me.txt"}}
	m = m.syncSearchTableRows()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(model)
	updated, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	next = updated.(model)

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

func TestDeleteConfirmationEnterStartsDeleteCommand(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModePermanent,
		AutoScanOnStart: true,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModePermanent,
		AutoScanOnStart: true,
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

func TestStartupViewIsMinimalByDefault(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewStartup
	m.busy = true
	m.activeScanLabel = "initial-scan"

	out := m.View()
	if !strings.Contains(out, "Scanning before search opens") || !strings.Contains(out, "Press Space for progress") {
		t.Fatalf("expected minimal startup scanning message, got:\n%s", out)
	}
	for _, hidden := range []string{"GoEverything", "status:", "keys:", "Scan location", "Search will open automatically", "PROGRESS", "scanned:"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("did not expect %q in minimal startup view, got:\n%s", hidden, out)
		}
	}
}

func TestStartupSpaceTogglesProgressDetails(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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
	if !next.showScanProgress {
		t.Fatalf("expected progress details to be shown")
	}
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
	if next.showScanProgress {
		t.Fatalf("expected progress details to be hidden")
	}
}

func TestStartupCtrlXCancelsAndOnlyCtrlQQuits(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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
	if next.searchListFocus {
		t.Fatalf("expected search input focus after startup scan")
	}
	if cmd == nil {
		t.Fatalf("expected count refresh command")
	}
}

func TestInitialScanFailureOpensConfig(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewStartup
	m.busy = true

	updated, cmd := m.Update(scanDoneMsg{label: "initial-scan", err: errors.New("permission denied")})
	next := updated.(model)

	if next.mode != viewConfig {
		t.Fatalf("expected config mode after failed startup scan, got %v", next.mode)
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

func TestCtrlGSwitchesToScanModal(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewSearch

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	next := updated.(model)

	if next.mode != viewStartup {
		t.Fatalf("expected ctrl+g to switch to scan modal, got %v", next.mode)
	}
	if !next.busy {
		t.Fatalf("expected ctrl+g to start scan")
	}
	if next.activeScanLabel != "manual-scan" {
		t.Fatalf("expected manual scan label, got %q", next.activeScanLabel)
	}
	if cmd == nil {
		t.Fatalf("expected scan command")
	}
	out := next.View()
	if !strings.Contains(out, "Scanning index") {
		t.Fatalf("expected manual scan modal message, got:\n%s", out)
	}
}

func TestEscFromConfigReturnsToSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

func modelWithManySearchRows() model {
	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModeTrash,
		AutoScanOnStart: false,
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
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git", "node_modules"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	}

	m := newModel(context.Background(), cfg)
	m.mode = viewMenu
	updated, _ := m.Update(mouseEventIn(m.menuCardHitbox(2), tea.MouseActionMotion, tea.MouseButtonNone))
	next := updated.(model)
	if next.hoveredMouse.kind != mouseTargetMenuCard || next.hoveredMouse.index != 2 {
		t.Fatalf("expected menu card hover, got %#v", next.hoveredMouse)
	}
	if next.mode != viewMenu || next.menuCursor != 0 {
		t.Fatalf("hover should not activate menu, mode=%v cursor=%d", next.mode, next.menuCursor)
	}

	m = newModel(context.Background(), cfg)
	m.mode = viewConfig
	m.cfgCursor = 0
	updated, _ = m.Update(mouseEventIn(m.configRowHitbox(1), tea.MouseActionMotion, tea.MouseButtonNone))
	next = updated.(model)
	if next.hoveredMouse.kind != mouseTargetConfigRow || next.hoveredMouse.index != 1 {
		t.Fatalf("expected config row hover, got %#v", next.hoveredMouse)
	}
	if next.cfgCursor != 0 || next.modal != noModal {
		t.Fatalf("hover should not activate config, cursor=%d modal=%v", next.cfgCursor, next.modal)
	}

	m = newModel(context.Background(), cfg)
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
	if next.searchCur != 0 || next.searchListFocus {
		t.Fatalf("hover should not select result, cursor=%d focus=%v", next.searchCur, next.searchListFocus)
	}
}

func TestSearchResultHitboxesMatchRenderedRows(t *testing.T) {
	t.Parallel()

	m := modelWithManySearchRows()
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

	m := modelWithManySearchRows()
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

func TestMouseLeftClickActivatesMenuAndConfigRows(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.mode = viewMenu
	updated, _ := m.Update(mouseEventIn(m.menuCardHitbox(2), tea.MouseActionPress, tea.MouseButtonLeft))
	next := updated.(model)
	if next.mode != viewConfig || next.menuCursor != 2 {
		t.Fatalf("expected menu config click to open config, mode=%v cursor=%d", next.mode, next.menuCursor)
	}

	m = newModel(context.Background(), m.cfg)
	m.mode = viewConfig
	updated, _ = m.Update(mouseEventIn(m.configRowHitbox(1), tea.MouseActionPress, tea.MouseButtonLeft))
	next = updated.(model)
	if next.modal != themeModal || next.cfgCursor != 1 {
		t.Fatalf("expected theme row click to open theme modal, modal=%v cursor=%d", next.modal, next.cfgCursor)
	}
}

func TestSearchSettingsMouseClickOpensConfig(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModeTrash,
		AutoScanOnStart: false,
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
	if !next.searchListFocus || next.searchCur != 1 || next.searchTable.Cursor() != 1 {
		t.Fatalf("expected first click to select row 1, focus=%v searchCur=%d tableCursor=%d", next.searchListFocus, next.searchCur, next.searchTable.Cursor())
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

func TestMouseWheelMovesSearchConfigAndModalCursors(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git", "node_modules"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m = newModel(context.Background(), m.cfg)
	m.mode = viewConfig
	updated, _ = m.Update(mouseEventIn(m.configRowHitbox(0), tea.MouseActionPress, tea.MouseButtonWheelDown))
	next = updated.(model)
	if next.cfgCursor != 1 {
		t.Fatalf("expected wheel down to move config cursor to 1, got %d", next.cfgCursor)
	}

	m = newModel(context.Background(), m.cfg)
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
	if strings.Contains(out, "status:") {
		t.Fatalf("did not expect status footer in config view, got:\n%s", out)
	}
}

func TestExcludeInputModalDoesNotOverflow(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})
	m.modal = excludeInputModal
	m.cfgInputActive = true
	m.cfgInputTarget = "exclude"
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

func TestConfigViewShowsAndTogglesDeleteMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		DeleteMode:      config.DeleteModeTrash,
		AutoScanOnStart: false,
	})
	m.mode = viewConfig

	out := m.View()
	if !strings.Contains(out, "delete mode") || !strings.Contains(out, "Trash") {
		t.Fatalf("expected delete mode setting rendered, got:\n%s", out)
	}

	m.cfgCursor = 2
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(model)
	if next.cfg.DeleteMode != config.DeleteModePermanent {
		t.Fatalf("expected delete mode to toggle to permanent, got %q", next.cfg.DeleteMode)
	}
}

func TestTopBarHidesIdleStatusWhenNotBusy(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
	})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	next := updated.(model)
	if next.width != 100 || next.height != 40 {
		t.Fatalf("expected size 100x40, got %dx%d", next.width, next.height)
	}
}

func TestSearchViewRendersTableResults(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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
}

func TestSearchCtrlPNoLongerTogglesPathFilter(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

func TestOnlyCtrlQQuitsFromSearch(t *testing.T) {
	t.Parallel()

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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

	m := newModel(context.Background(), config.Config{
		DBPath:          "/tmp/test.db",
		DefaultScanPath: "~",
		Excludes:        []string{".git"},
		Theme:           "tokyonight",
		AutoScanOnStart: false,
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
