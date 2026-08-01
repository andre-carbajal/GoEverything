package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goeverything/internal/config"
	"goeverything/internal/scanner"
)

func (m model) openLocationPickerView(label string) model {
	m.modal = noModal
	m.mode = viewLocation
	m.locationRoots = quickLocationRoots()
	m.locationRootCursor = min(max(0, m.locationRootCursor), max(0, len(m.locationRoots)-1))
	m.locationSuggestions = nil
	m.locationSuggestionCursor = -1
	m.locationSuggestionActive = false
	m.locationInput.SetValue("")
	m.locationInput.CursorEnd()
	m.locationInputSeq++
	m.locationScanLabel = label
	m.locationInput.Focus()
	m.searchInput.Blur()
	m.searchInput.SetValue("")
	m.searchRes = nil
	m.searchCur = 0
	m = m.syncSearchTableRows()
	m.searchTable.Blur()
	return m
}

func quickLocationRoots() []string {
	seen := map[string]struct{}{"~": {}}
	roots := []string{"~"}
	for _, root := range scanner.DiscoverRoots() {
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func (m model) selectLocationRoot(index int) (model, tea.Cmd) {
	if index < 0 || index >= len(m.locationRoots) {
		m.err = fmt.Errorf("no accessible volumes or drives found")
		return m, nil
	}
	m.locationRootCursor = index
	return m.confirmLocationPath(m.locationRoots[index])
}

func (m model) updateLocation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	inputEmpty := strings.TrimSpace(m.locationInput.Value()) == ""

	switch msg.String() {
	case "esc":
		m.locationInput.Blur()
		m.err = nil
		if m.locationScanLabel == "manual-scan" {
			return m.focusSearchView(), nil
		}
		return m, tea.Quit
	case "up":
		if inputEmpty {
			m.locationRootCursor = max(0, m.locationRootCursor-1)
		} else if len(m.locationSuggestions) > 0 {
			m.locationSuggestionActive = true
			if m.locationSuggestionCursor < 0 {
				m.locationSuggestionCursor = 0
			} else {
				m.locationSuggestionCursor = max(0, m.locationSuggestionCursor-1)
			}
		}
		return m, nil
	case "down":
		if inputEmpty {
			m.locationRootCursor = min(max(0, len(m.locationRoots)-1), m.locationRootCursor+1)
		} else if len(m.locationSuggestions) > 0 {
			m.locationSuggestionActive = true
			m.locationSuggestionCursor = min(len(m.locationSuggestions)-1, max(0, m.locationSuggestionCursor+1))
		}
		return m, nil
	case "tab", "right":
		if !inputEmpty && len(m.locationSuggestions) > 0 {
			index := m.locationSuggestionCursor
			if index < 0 {
				index = 0
			}
			return m.acceptLocationSuggestion(index)
		}
		return m, nil
	case "enter":
		if inputEmpty {
			return m.selectLocationRoot(m.locationRootCursor)
		}
		if m.locationSuggestionActive && m.locationSuggestionCursor >= 0 && m.locationSuggestionCursor < len(m.locationSuggestions) {
			return m.confirmLocationPath(m.locationSuggestions[m.locationSuggestionCursor])
		}
		return m.confirmLocation()
	}

	var cmd tea.Cmd
	m.locationInput, cmd = m.locationInput.Update(msg)
	m.locationInputSeq++
	m.locationSuggestionCursor = -1
	m.locationSuggestionActive = false
	m.err = nil
	return m, tea.Batch(cmd, locationSuggestionsCmd(m.locationInputSeq, m.locationInput.Value()))
}

func (m model) acceptLocationSuggestion(index int) (model, tea.Cmd) {
	if index < 0 || index >= len(m.locationSuggestions) {
		return m, nil
	}
	m.locationInput.SetValue(m.locationSuggestions[index])
	m.locationInput.CursorEnd()
	m.locationSuggestionCursor = index
	m.locationSuggestionActive = true
	m.locationInputSeq++
	m.err = nil
	return m, locationSuggestionsCmd(m.locationInputSeq, m.locationInput.Value())
}

func (m model) confirmLocation() (model, tea.Cmd) {
	return m.confirmLocationPath(strings.TrimSpace(m.locationInput.Value()))
}

func (m model) confirmLocationPath(value string) (model, tea.Cmd) {
	value = strings.TrimSpace(value)
	if value == "" {
		m.err = fmt.Errorf("choose a volume or enter a folder path")
		return m, nil
	}

	root, err := config.ExpandPath(value)
	if err != nil {
		m.err = err
		return m, nil
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		m.err = err
		return m, nil
	}
	if !info.IsDir() {
		m.err = fmt.Errorf("%s is not a directory", value)
		return m, nil
	}

	m.activeScanRoot = root
	m.err = nil
	m.startupScanAttempted = true
	if m.locationScanLabel == "manual-scan" {
		m.status = "manual scan in progress…"
	} else {
		m.status = "initial scan in progress…"
	}
	m.mode = viewStartup
	return m.startScanCmd([]string{root}, m.locationScanLabel, false)
}

func (m model) scrollLocation(delta int) model {
	if strings.TrimSpace(m.locationInput.Value()) == "" {
		m.locationRootCursor = min(max(0, m.locationRootCursor+delta), max(0, len(m.locationRoots)-1))
		return m
	}
	if len(m.locationSuggestions) > 0 {
		m.locationSuggestionActive = true
		m.locationSuggestionCursor = min(max(0, m.locationSuggestionCursor+delta), len(m.locationSuggestions)-1)
	}
	return m
}

func (m model) viewLocation(width, height int) string {
	lines := []string{
		m.theme.Title.Render("SELECT LOCATION TO SCAN"),
		m.theme.Muted.Render("Type a folder path or choose a volume/drive. Esc exits without scanning."),
		"",
		m.locationInputView(width),
	}

	if strings.TrimSpace(m.locationInput.Value()) == "" {
		lines = append(lines, "", m.theme.Title.Render("QUICK LOCATIONS"))
		start, end := locationVisibleRange(len(m.locationRoots), m.locationRootCursor, max(1, height-10))
		for i := start; i < end; i++ {
			root := m.locationRoots[i]
			prefix := "  "
			style := m.theme.Text
			if i == m.locationRootCursor {
				prefix = "➜ "
				style = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
			} else if m.mouseHoverMatches(mouseTargetLocationRoot, i) {
				style = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SurfaceBG)
			}
			lines = append(lines, style.Render(prefix+locationRootLabel(root)))
		}
	} else {
		lines = append(lines, "", m.theme.Title.Render("FOLDERS"))
		if len(m.locationSuggestions) == 0 {
			lines = append(lines, m.theme.Muted.Render("No accessible folders match this path."))
		} else {
			start, end := locationVisibleRange(len(m.locationSuggestions), m.locationSuggestionCursor, max(1, height-10))
			for i := start; i < end; i++ {
				suggestion := m.locationSuggestions[i]
				prefix := "  "
				style := m.theme.Text
				if m.locationSuggestionActive && i == m.locationSuggestionCursor {
					prefix = "➜ "
					style = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
				} else if m.mouseHoverMatches(mouseTargetLocationSuggestion, i) {
					style = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SurfaceBG)
				}
				lines = append(lines, style.Render(prefix+trimMiddle(suggestion, max(16, min(96, width-16)))))
			}
		}
	}

	if m.err != nil {
		lines = append(lines, "", m.theme.Err.Render(trimMiddle("error: "+m.err.Error(), max(20, width-12))))
	}

	cardHeight := min(max(8, len(lines)+2), max(8, height-2))
	card := m.panelStyle().
		Width(max(36, min(100, width-4))).
		Height(cardHeight).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, card)
}

func (m model) locationInputView(width int) string {
	cardW := max(36, min(100, width-4))
	contentW := max(20, cardW-4)
	inputW := max(12, contentW-4)
	input := m.locationInput
	input.Width = inputW
	input.CursorEnd()
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Input).
		Padding(0, 1).
		Width(contentW).
		Render(input.View())
}

func locationRootLabel(root string) string {
	if root == "~" {
		return "Home (~)"
	}
	if root == string(filepath.Separator) {
		return "System (" + root + ")"
	}
	return root
}

func locationInputHitbox(m model) hitbox {
	box := locationCardHitbox(m)
	return hitbox{x: box.x + 2, y: box.y + 4, w: max(1, box.w-4), h: 3}
}

func (m model) locationInputHitbox() hitbox {
	return locationInputHitbox(m)
}

func (m model) locationRootHitbox(index int) hitbox {
	if strings.TrimSpace(m.locationInput.Value()) != "" {
		return hitbox{}
	}
	box := locationCardHitbox(m)
	_, bodyH := m.bodySize()
	start, end := locationVisibleRange(len(m.locationRoots), m.locationRootCursor, max(1, bodyH-10))
	if index < start || index >= end {
		return hitbox{}
	}
	return hitbox{x: box.x + 2, y: box.y + 9 + index - start, w: max(1, box.w-4), h: 1}
}

func (m model) locationSuggestionHitbox(index int) hitbox {
	if strings.TrimSpace(m.locationInput.Value()) == "" {
		return hitbox{}
	}
	box := locationCardHitbox(m)
	_, bodyH := m.bodySize()
	start, end := locationVisibleRange(len(m.locationSuggestions), m.locationSuggestionCursor, max(1, bodyH-10))
	if index < start || index >= end {
		return hitbox{}
	}
	return hitbox{x: box.x + 2, y: box.y + 9 + index - start, w: max(1, box.w-4), h: 1}
}

func locationCardHitbox(m model) hitbox {
	bodyW, bodyH := m.bodySize()
	cardW := max(36, min(100, bodyW-4))
	errorHeight := 0
	if m.err != nil {
		errorHeight = 2
	}
	listLength := len(m.locationRoots)
	listCursor := m.locationRootCursor
	if strings.TrimSpace(m.locationInput.Value()) != "" {
		listLength = len(m.locationSuggestions)
		listCursor = m.locationSuggestionCursor
	}
	start, end := locationVisibleRange(listLength, listCursor, max(1, bodyH-10))
	listHeight := end - start
	if listLength == 0 {
		listHeight = 1
	}
	cardH := min(max(8, listHeight+9+errorHeight), max(8, bodyH-2))
	return hitbox{
		x: m.contentStartX() + max(0, (bodyW-cardW)/2),
		y: 1 + max(0, (bodyH-cardH)/2),
		w: cardW,
		h: cardH,
	}
}

func locationVisibleRange(total, cursor, capacity int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	capacity = max(1, capacity)
	cursor = min(max(0, cursor), total-1)
	start := 0
	if cursor >= capacity {
		start = cursor - capacity + 1
	}
	end := min(total, start+capacity)
	if end-start < capacity {
		start = max(0, end-capacity)
	}
	return start, end
}

func locationSuggestionsCmd(seq int, input string) tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		suggestions, err := locationSuggestions(input)
		return locationSuggestionsMsg{seq: seq, input: input, suggestions: suggestions, err: err}
	})
}

func locationSuggestions(input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil
	}
	parent, prefix := locationCompletionParts(input)
	if parent == "" {
		return nil, nil
	}
	resolvedParent, err := config.ExpandPath(parent)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolvedParent)
	if err != nil {
		return nil, err
	}

	prefix = strings.ToLower(prefix)
	suggestions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(strings.ToLower(entry.Name()), prefix) {
			continue
		}
		suggestions = append(suggestions, filepath.Join(parent, entry.Name()))
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return strings.ToLower(suggestions[i]) < strings.ToLower(suggestions[j])
	})
	return suggestions, nil
}

func locationCompletionParts(input string) (string, string) {
	p := filepath.FromSlash(strings.TrimSpace(input))
	if p == "" {
		return "", ""
	}
	if p == "~" {
		return p, ""
	}
	if volume := filepath.VolumeName(p); volume != "" && p == volume {
		return p + string(filepath.Separator), ""
	}
	if p == "~" || strings.HasSuffix(p, string(filepath.Separator)) {
		return p, ""
	}
	return filepath.Dir(p), filepath.Base(p)
}
