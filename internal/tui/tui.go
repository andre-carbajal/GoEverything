package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"goeverything/internal/config"
	"goeverything/internal/db"
	"goeverything/internal/scanner"
	"goeverything/internal/watcher"
)

type viewMode int

const (
	viewMenu viewMode = iota
	viewSearch
	viewConfig
)

type searchDoneMsg struct {
	query   string
	results []db.Entry
	err     error
}

type countDoneMsg struct {
	total int64
	err   error
}

type reindexDoneMsg struct {
	metrics scanner.Metrics
	err     error
}

type scanDoneMsg struct {
	metrics scanner.Metrics
	err     error
	label   string
}

type debounceSearchMsg struct {
	seq   int
	query string
}

type model struct {
	ctx context.Context

	cfg config.Config

	mode       viewMode
	menu       []string
	menuCursor int

	searchInput textinput.Model
	searchSeq   int
	searchRes   []db.Entry
	searchCur   int
	tempRoots   []string

	addRootInput    textinput.Model
	addRootActive   bool
	searchListFocus bool

	cfgCursor      int
	cfgInput       textinput.Model
	cfgInputActive bool
	cfgThemePicker bool
	cfgThemeCursor int
	cfgPathPicker  bool
	cfgPathCursor  int
	pathOptions    []string

	theme        theme
	themes       []string
	totalIndexed int64
	lastMetrics  scanner.Metrics

	busy   bool
	status string
	err    error
}

func Run(ctx context.Context, cfg config.Config) error {
	m := newModel(ctx, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func newModel(ctx context.Context, cfg config.Config) model {
	searchInput := textinput.New()
	searchInput.Placeholder = "Type to search… (/ to focus)"
	searchInput.Prompt = "search> "
	searchInput.Focus()
	searchInput.CharLimit = 512
	searchInput.Width = 60

	cfgInput := textinput.New()
	cfgInput.Placeholder = "Enter custom scan path (example: ~/Projects)"
	cfgInput.Prompt = "path> "
	cfgInput.CharLimit = 300
	cfgInput.Width = 60

	addRootInput := textinput.New()
	addRootInput.Placeholder = "Add temporary folder (example: ~/Projects)"
	addRootInput.Prompt = "temp-root> "
	addRootInput.CharLimit = 512
	addRootInput.Width = 60

	return model{
		ctx:          ctx,
		cfg:          cfg,
		mode:         viewSearch,
		menu:         []string{"Search", "Re-index", "Config"},
		searchInput:  searchInput,
		addRootInput: addRootInput,
		cfgInput:     cfgInput,
		theme:        themeByName(cfg.Theme),
		themes:       []string{"tokyonight", "catppuccin", "groovbox"},
		pathOptions:  availablePathOptions(),
		status:       "ready",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(countCmd(m.ctx, m.cfg.DBPath), textinput.Blink)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.searchInput.Width = max(30, msg.Width-18)
		m.cfgInput.Width = max(30, msg.Width-20)
		m.addRootInput.Width = max(30, msg.Width-22)
		return m, nil

	case countDoneMsg:
		if msg.err == nil {
			m.totalIndexed = msg.total
		}
		if msg.err == nil && msg.total == 0 && m.cfg.AutoScanOnStart && !m.busy {
			roots, err := defaultScanRoots(m.cfg)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.busy = true
			m.status = "initial scan in progress…"
			return m, scanRootsCmd(m.ctx, m.cfg, roots, "initial-scan")
		}
		return m, nil

	case searchDoneMsg:
		if msg.query == m.searchInput.Value() {
			m.searchRes = msg.results
			if m.searchCur >= len(m.searchRes) {
				m.searchCur = max(0, len(m.searchRes)-1)
			}
		}
		m.err = msg.err
		return m, nil

	case reindexDoneMsg:
		m.busy = false
		m.lastMetrics = msg.metrics
		m.err = msg.err
		if msg.err == nil {
			m.status = fmt.Sprintf("re-index done: scanned=%d indexed=%d", msg.metrics.Scanned, msg.metrics.Indexed)
		} else {
			m.status = "re-index failed"
		}
		return m, countCmd(m.ctx, m.cfg.DBPath)

	case scanDoneMsg:
		m.busy = false
		m.lastMetrics = msg.metrics
		m.err = msg.err
		if msg.err == nil {
			m.status = fmt.Sprintf("%s done: scanned=%d indexed=%d", msg.label, msg.metrics.Scanned, msg.metrics.Indexed)
		} else {
			m.status = msg.label + " failed"
		}
		return m, countCmd(m.ctx, m.cfg.DBPath)

	case debounceSearchMsg:
		if msg.seq != m.searchSeq {
			return m, nil
		}
		if strings.TrimSpace(msg.query) == "" {
			m.searchRes = nil
			return m, nil
		}
		return m, searchCmd(m.ctx, m.cfg.DBPath, msg.query)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc":
			if m.cfgInputActive {
				m.cfgInputActive = false
				m.cfgInput.Blur()
				m.cfgInput.SetValue("")
				return m, nil
			}
			if m.mode != viewMenu {
				m.mode = viewMenu
				m.status = "back to menu"
				return m, nil
			}
		case "/":
			if m.mode != viewSearch {
				m.mode = viewSearch
			}
			m.searchListFocus = false
			m.searchInput.Focus()
			return m, nil
		}

		switch m.mode {
		case viewMenu:
			return m.updateMenu(msg)
		case viewSearch:
			return m.updateSearch(msg)
		case viewConfig:
			return m.updateConfig(msg)
		}
	}

	return m, nil
}

func (m model) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.menuCursor = (m.menuCursor + 1) % len(m.menu)
	case "k", "up":
		m.menuCursor = (m.menuCursor - 1 + len(m.menu)) % len(m.menu)
	case "enter":
		switch m.menu[m.menuCursor] {
		case "Search":
			m.mode = viewSearch
			m.searchListFocus = false
			m.searchInput.Focus()
			return m, nil
		case "Re-index":
			if m.busy {
				return m, nil
			}
			roots, err := defaultScanRoots(m.cfg)
			if err != nil {
				m.err = err
				m.status = "re-index failed"
				return m, nil
			}
			m.busy = true
			m.status = "re-indexing…"
			return m, reindexCmd(m.ctx, m.cfg, roots)
		case "Config":
			m.mode = viewConfig
			return m, nil
		}
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.addRootActive {
		switch msg.String() {
		case "enter":
			raw := strings.TrimSpace(m.addRootInput.Value())
			m.addRootInput.SetValue("")
			m.addRootActive = false
			m.addRootInput.Blur()
			if raw == "" {
				return m, nil
			}
			root, err := config.ExpandPath(raw)
			if err != nil {
				m.err = err
				return m, nil
			}
			info, statErr := os.Stat(root)
			if statErr != nil {
				m.err = statErr
				return m, nil
			}
			if !info.IsDir() {
				m.err = fmt.Errorf("temporary root %q is not a directory", root)
				return m, nil
			}
			if !slices.Contains(m.tempRoots, root) {
				m.tempRoots = append(m.tempRoots, root)
			}
			m.busy = true
			m.status = "scanning temporary root…"
			return m, scanRootsCmd(m.ctx, m.cfg, []string{root}, "temp-scan")
		case "esc":
			m.addRootInput.SetValue("")
			m.addRootActive = false
			m.addRootInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.addRootInput, cmd = m.addRootInput.Update(msg)
		return m, cmd
	}

	if msg.String() == "tab" {
		m.searchListFocus = !m.searchListFocus
		if m.searchListFocus {
			m.searchInput.Blur()
		} else {
			m.searchInput.Focus()
		}
		return m, nil
	}

	if m.searchListFocus {
		switch msg.String() {
		case "j", "down":
			if len(m.searchRes) > 0 && m.searchCur < len(m.searchRes)-1 {
				m.searchCur++
			}
			return m, nil
		case "k", "up":
			if m.searchCur > 0 {
				m.searchCur--
			}
			return m, nil
		case "enter":
			if len(m.searchRes) > 0 {
				path := m.searchRes[m.searchCur].Path
				return m, openCmd(path, true)
			}
			return m, nil
		case "ctrl+a":
			m.addRootActive = true
			m.addRootInput.SetValue("")
			return m, m.addRootInput.Focus()
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+a":
		m.addRootActive = true
		m.addRootInput.SetValue("")
		return m, m.addRootInput.Focus()
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchSeq++
	q := m.searchInput.Value()
	return m, tea.Batch(cmd, debounceCmd(m.searchSeq, q))
}

func (m model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cfgInputActive {
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(m.cfgInput.Value())
			m.cfgInput.SetValue("")
			m.cfgInputActive = false
			m.cfgInput.Blur()
			if value == "" {
				return m, nil
			}
			if m.cfgCursor == 0 {
				root, err := config.ExpandPath(value)
				if err != nil {
					m.err = err
					return m, nil
				}
				info, statErr := os.Stat(root)
				if statErr != nil || !info.IsDir() {
					if statErr != nil {
						m.err = statErr
					} else {
						m.err = fmt.Errorf("%s is not a directory", root)
					}
					return m, nil
				}
				m.cfg.DefaultScanPath = value
				if err := config.Save(m.cfg); err != nil {
					m.err = err
				} else {
					m.status = "saved scan location"
				}
				return m, nil
			}
			if !slices.Contains(m.cfg.Excludes, value) {
				m.cfg.Excludes = append(m.cfg.Excludes, value)
				slices.Sort(m.cfg.Excludes)
				if err := config.Save(m.cfg); err != nil {
					m.err = err
				} else {
					m.status = "exclude added"
				}
			}
			return m, nil
		case "esc":
			m.cfgInputActive = false
			m.cfgInput.Blur()
			m.cfgInput.SetValue("")
			return m, nil
		}
		var cmd tea.Cmd
		m.cfgInput, cmd = m.cfgInput.Update(msg)
		return m, cmd
	}

	if m.cfgThemePicker {
		switch msg.String() {
		case "j", "down":
			m.cfgThemeCursor = min(len(m.themes)-1, m.cfgThemeCursor+1)
		case "k", "up":
			m.cfgThemeCursor = max(0, m.cfgThemeCursor-1)
		case "enter":
			m.cfg.Theme = m.themes[m.cfgThemeCursor]
			m.theme = themeByName(m.cfg.Theme)
			m.cfgThemePicker = false
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "theme updated"
			}
		case "esc":
			m.cfgThemePicker = false
		}
		return m, nil
	}

	if m.cfgPathPicker {
		switch msg.String() {
		case "j", "down":
			m.cfgPathCursor = min(len(m.pathOptions)-1, m.cfgPathCursor+1)
		case "k", "up":
			m.cfgPathCursor = max(0, m.cfgPathCursor-1)
		case "enter":
			choice := m.pathOptions[m.cfgPathCursor]
			if choice == "__custom__" {
				m.cfgPathPicker = false
				m.cfgInputActive = true
				m.cfgInput.SetValue("")
				m.cfgInput.Placeholder = "Custom scan path (example: ~/Projects)"
				m.cfgInput.Prompt = "scan-path> "
				return m, m.cfgInput.Focus()
			}
			m.cfg.DefaultScanPath = choice
			m.cfgPathPicker = false
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "saved scan location"
			}
		case "esc":
			m.cfgPathPicker = false
		}
		return m, nil
	}

	maxCursor := 2 + len(m.cfg.Excludes)
	switch msg.String() {
	case "j", "down":
		m.cfgCursor = min(maxCursor, m.cfgCursor+1)
	case "k", "up":
		m.cfgCursor = max(0, m.cfgCursor-1)
	case "a":
		m.cfgInputActive = true
		m.cfgInput.Placeholder = "Add exclude (example: .git or Library/Caches/*)"
		m.cfgInput.Prompt = "exclude> "
		return m, m.cfgInput.Focus()
	case "enter":
		switch m.cfgCursor {
		case 0:
			m.cfgPathPicker = true
			m.cfgPathCursor = 0
		case 1:
			m.cfg.AutoScanOnStart = !m.cfg.AutoScanOnStart
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "auto-scan updated"
			}
		case 2:
			m.cfgThemePicker = true
			idx := slices.Index(m.themes, m.cfg.Theme)
			if idx < 0 {
				idx = 0
			}
			m.cfgThemeCursor = idx
		}
	case "d":
		exIdx := m.cfgCursor - 3
		if exIdx < 0 || exIdx >= len(m.cfg.Excludes) {
			return m, nil
		}
		m.cfg.Excludes = append(m.cfg.Excludes[:exIdx], m.cfg.Excludes[exIdx+1:]...)
		m.cfgCursor = min(m.cfgCursor, 2+max(0, len(m.cfg.Excludes)-1))
		if len(m.cfg.Excludes) == 0 {
			m.cfg.Excludes = scanner.DefaultExcludes()
		}
		if err := config.Save(m.cfg); err != nil {
			m.err = err
		} else {
			m.status = "exclude removed"
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.viewHeader())
	b.WriteString("\n")
	switch m.mode {
	case viewMenu:
		b.WriteString(m.viewMenu())
	case viewSearch:
		b.WriteString(m.viewSearch())
	case viewConfig:
		b.WriteString(m.viewConfig())
	}
	b.WriteString("\n")
	if m.busy {
		b.WriteString(m.theme.Warn.Render("busy: " + m.status))
	} else if m.err != nil {
		b.WriteString(m.theme.Err.Render("error: " + m.err.Error()))
	} else {
		b.WriteString(m.theme.Muted.Render("status: " + m.status))
	}
	b.WriteString("\n")
	b.WriteString(m.theme.Muted.Render("keys: tab focus input/list • j/k move (list) • enter open folder (list) • ctrl+a add temp folder • / focus search • esc back • q quit"))
	return m.theme.Container.Render(b.String())
}

func (m model) viewHeader() string {
	ascii := []string{
		"   █████████           ██████████                                            █████    █████       ███                     ",
		"  ███░░░░░███         ░░███░░░░░█                                           ░░███    ░░███       ░░░                      ",
		" ███     ░░░   ██████  ░███  █ ░  █████ █████  ██████  ████████  █████ ████ ███████   ░███████   ████  ████████    ███████",
		"░███          ███░░███ ░██████   ░░███ ░░███  ███░░███░░███░░███░░███ ░███ ░░░███░    ░███░░███ ░░███ ░░███░░███  ███░░███",
		"░███    █████░███ ░███ ░███░░█    ░███  ░███ ░███████  ░███ ░░░  ░███ ░███   ░███     ░███ ░███  ░███  ░███ ░███ ░███ ░███",
		"░░███  ░░███ ░███ ░███ ░███ ░   █ ░░███ ███  ░███░░░   ░███      ░███ ░███   ░███ ███ ░███ ░███  ░███  ░███ ░███ ░███ ░███",
		" ░░█████████ ░░██████  ██████████  ░░█████   ░░██████  █████     ░░███████   ░░█████  ████ █████ █████ ████ █████░░███████",
		"  ░░░░░░░░░   ░░░░░░  ░░░░░░░░░░    ░░░░░     ░░░░░░  ░░░░░       ░░░░░███    ░░░░░  ░░░░ ░░░░░ ░░░░░ ░░░░ ░░░░░  ░░░░░███",
		"                                                                  ███ ░███                                        ███ ░███",
		"                                                                 ░░██████                                        ░░██████ ",
		"                                                                  ░░░░░░                                          ░░░░░░  ",
	}
	line := fmt.Sprintf(" indexed=%d  scan_path=%s  last_scan=%d files in %s  theme=%s",
		m.totalIndexed, m.cfg.DefaultScanPath, m.lastMetrics.Indexed, m.lastMetrics.Elapsed, m.cfg.Theme)
	return m.theme.Header.Render(strings.Join(ascii, "\n")) + "\n" + m.theme.Highlight.Render(line)
}

func (m model) viewMenu() string {
	var lines []string
	lines = append(lines, m.theme.Title.Render("Main Menu"))
	for i, item := range m.menu {
		prefix := "  "
		style := m.theme.Text
		if i == m.menuCursor {
			prefix = "➜ "
			style = m.theme.Highlight
		}
		lines = append(lines, style.Render(prefix+item))
	}
	return strings.Join(lines, "\n")
}

func (m model) viewSearch() string {
	var lines []string
	lines = append(lines, m.theme.Title.Render("Search"))
	lines = append(lines, m.theme.Muted.Render("scope: "+m.searchScopeLabel()))
	inputStyle := lipgloss.NewStyle().Padding(0, 1)
	if !m.searchListFocus {
		inputStyle = inputStyle.
			Background(lipgloss.Color("#2f3b63")).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true)
	} else {
		inputStyle = inputStyle.Foreground(lipgloss.Color("#8b93b8"))
	}
	lines = append(lines, inputStyle.Render(m.searchInput.View()))
	if m.addRootActive {
		lines = append(lines, m.addRootInput.View())
	}
	lines = append(lines, "")
	if len(m.searchRes) == 0 {
		lines = append(lines, m.theme.Muted.Render("No results"))
		return strings.Join(lines, "\n")
	}
	maxRows := 18
	start := max(0, m.searchCur-maxRows/2)
	end := min(len(m.searchRes), start+maxRows)
	for i := start; i < end; i++ {
		entry := m.searchRes[i]
		prefix := "  "
		style := m.theme.Text
		if i == m.searchCur {
			prefix = "➜ "
			if m.searchListFocus {
				style = lipgloss.NewStyle().
					Background(lipgloss.Color("#2f3b63")).
					Foreground(lipgloss.Color("#ffffff")).
					Bold(true)
			} else {
				style = m.theme.Highlight
			}
		}
		kind := "f"
		if entry.IsDir {
			kind = "d"
		}
		row := fmt.Sprintf("%s[%s] %s  %s", prefix, kind, entry.Name, filepath.Dir(entry.Path))
		lines = append(lines, style.Render(row))
	}
	return strings.Join(lines, "\n")
}

func (m model) viewConfig() string {
	var lines []string
	lines = append(lines, m.theme.Title.Render("Config"))
	rowStyle := func(idx int) lipgloss.Style {
		if m.cfgCursor == idx {
			return lipgloss.NewStyle().Background(lipgloss.Color("#2f3b63")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("#c0caf5"))
	}
	autoYes := "No"
	if m.cfg.AutoScanOnStart {
		autoYes = "Yes"
	}
	lines = append(lines, rowStyle(0).Render("Scan Location: "+m.cfg.DefaultScanPath+"  (Enter to change)"))
	lines = append(lines, rowStyle(1).Render("Auto Scan on Start: "+autoYes+"  (Enter to toggle)"))
	lines = append(lines, rowStyle(2).Render("Theme: "+m.cfg.Theme+"  (Enter to select)"))
	if m.cfgPathPicker {
		lines = append(lines, "")
		lines = append(lines, m.theme.Title.Render("Select scan location (Enter to confirm, Esc to cancel)"))
		for i, p := range m.pathOptions {
			style := m.theme.Text
			prefix := "  "
			label := p
			if p == "__custom__" {
				label = "Custom path..."
			}
			if i == m.cfgPathCursor {
				prefix = "➜ "
				style = lipgloss.NewStyle().Background(lipgloss.Color("#2f3b63")).Foreground(lipgloss.Color("#ffffff")).Bold(true)
			}
			lines = append(lines, style.Render(prefix+label))
		}
	}
	if m.cfgThemePicker {
		lines = append(lines, "")
		lines = append(lines, m.theme.Title.Render("Select theme (Enter to confirm, Esc to cancel)"))
		for i, th := range m.themes {
			style := m.theme.Text
			prefix := "  "
			if i == m.cfgThemeCursor {
				prefix = "➜ "
				style = lipgloss.NewStyle().Background(lipgloss.Color("#2f3b63")).Foreground(lipgloss.Color("#ffffff")).Bold(true)
			}
			lines = append(lines, style.Render(prefix+th))
		}
	}
	lines = append(lines, "")
	lines = append(lines, m.theme.Title.Render("excludes (a=add, d=remove):"))
	if len(m.cfg.Excludes) == 0 {
		lines = append(lines, m.theme.Muted.Render("  <none>"))
	} else {
		for i, ex := range m.cfg.Excludes {
			cursor := "  "
			style := m.theme.Text
			if i+3 == m.cfgCursor {
				cursor = "➜ "
				style = m.theme.Highlight
			}
			lines = append(lines, style.Render(cursor+ex))
		}
	}
	if m.cfgInputActive {
		lines = append(lines, "")
		lines = append(lines, m.cfgInput.View())
	}
	return strings.Join(lines, "\n")
}

func searchCmd(ctx context.Context, dbPath, query string) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, dbPath)
		if err != nil {
			return searchDoneMsg{query: query, err: err}
		}
		defer store.Close()
		res, err := store.SearchAdvanced(ctx, db.SearchOptions{
			Query:  query,
			Limit:  100,
			Offset: 0,
		})
		return searchDoneMsg{query: query, results: res, err: err}
	}
}

func countCmd(ctx context.Context, dbPath string) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, dbPath)
		if err != nil {
			return countDoneMsg{err: err}
		}
		defer store.Close()
		total, err := store.Count(ctx)
		return countDoneMsg{total: total, err: err}
	}
}

func debounceCmd(seq int, query string) tea.Cmd {
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return debounceSearchMsg{seq: seq, query: query}
	})
}

func reindexCmd(ctx context.Context, cfg config.Config, roots []string) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return reindexDoneMsg{err: err}
		}
		defer store.Close()

		if err := store.ReindexFTS(ctx); err != nil {
			return reindexDoneMsg{err: err}
		}
		r := scanner.Runner{
			Indexer: store,
			Workers: scanner.DefaultWorkerCount(),
			Batch:   2000,
			Exclude: cfg.Excludes,
		}
		metrics, err := r.Scan(ctx, roots)
		return reindexDoneMsg{metrics: metrics, err: watcher.WithPermissionHint(err)}
	}
}

func scanRootsCmd(ctx context.Context, cfg config.Config, roots []string, label string) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return scanDoneMsg{err: err, label: label}
		}
		defer store.Close()

		r := scanner.Runner{
			Indexer: store,
			Workers: scanner.DefaultWorkerCount(),
			Batch:   2000,
			Exclude: cfg.Excludes,
		}
		metrics, err := r.Scan(ctx, roots)
		return scanDoneMsg{metrics: metrics, err: watcher.WithPermissionHint(err), label: label}
	}
}

func openCmd(path string, reveal bool) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		if reveal {
			cmd = exec.Command("open", "-R", path)
		} else {
			cmd = exec.Command("open", path)
		}
		_ = cmd.Start()
		return nil
	}
}

func (m model) searchScopeLabel() string {
	base := []string{m.cfg.DefaultScanPath}
	if len(m.tempRoots) == 0 {
		return strings.Join(base, ", ")
	}
	return strings.Join(base, ", ") + " + temp(" + strings.Join(m.tempRoots, ", ") + ")"
}

func defaultScanRoots(cfg config.Config) ([]string, error) {
	root, err := config.ExpandPath(cfg.DefaultScanPath)
	if err != nil {
		return nil, err
	}
	return []string{root}, nil
}

func availablePathOptions() []string {
	opts := []string{"~", "__custom__"}
	for _, root := range scanner.DiscoverRoots() {
		if root == "/" || root == "~" {
			continue
		}
		opts = append(opts, root)
	}
	return opts
}

type theme struct {
	Container lipgloss.Style
	Header    lipgloss.Style
	Title     lipgloss.Style
	Text      lipgloss.Style
	Muted     lipgloss.Style
	Highlight lipgloss.Style
	Err       lipgloss.Style
	Warn      lipgloss.Style
}

func themeByName(name string) theme {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "groovbox":
		return theme{
			Container: lipgloss.NewStyle().Padding(1, 2),
			Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("#fabd2f")),
			Title:     lipgloss.NewStyle().Foreground(lipgloss.Color("#b8bb26")).Bold(true),
			Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("#ebdbb2")),
			Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("#a89984")),
			Highlight: lipgloss.NewStyle().Foreground(lipgloss.Color("#83a598")).Bold(true),
			Err:       lipgloss.NewStyle().Foreground(lipgloss.Color("#fb4934")).Bold(true),
			Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("#fe8019")).Bold(true),
		}
	case "catppuccin":
		return theme{
			Container: lipgloss.NewStyle().Padding(1, 2),
			Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("#f5c2e7")),
			Title:     lipgloss.NewStyle().Foreground(lipgloss.Color("#89dceb")).Bold(true),
			Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4")),
			Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8")),
			Highlight: lipgloss.NewStyle().Foreground(lipgloss.Color("#94e2d5")).Bold(true),
			Err:       lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Bold(true),
			Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Bold(true),
		}
	default:
		return theme{
			Container: lipgloss.NewStyle().Padding(1, 2),
			Header:    lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7")),
			Title:     lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true),
			Text:      lipgloss.NewStyle().Foreground(lipgloss.Color("#c0caf5")),
			Muted:     lipgloss.NewStyle().Foreground(lipgloss.Color("#565f89")),
			Highlight: lipgloss.NewStyle().Foreground(lipgloss.Color("#9ece6a")).Bold(true),
			Err:       lipgloss.NewStyle().Foreground(lipgloss.Color("#f7768e")).Bold(true),
			Warn:      lipgloss.NewStyle().Foreground(lipgloss.Color("#e0af68")).Bold(true),
		}
	}
}
