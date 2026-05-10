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
	viewVolumes
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

	availableRoots []string
	volCursor      int

	cfgCursor      int
	cfgInput       textinput.Model
	cfgInputActive bool

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
	cfgInput.Placeholder = "Add exclude (example: .git or Library/Caches/*)"
	cfgInput.Prompt = "exclude> "
	cfgInput.CharLimit = 300
	cfgInput.Width = 60

	addRootInput := textinput.New()
	addRootInput.Placeholder = "Add temporary folder (example: ~/Projects)"
	addRootInput.Prompt = "temp-root> "
	addRootInput.CharLimit = 512
	addRootInput.Width = 60

	return model{
		ctx:            ctx,
		cfg:            cfg,
		mode:           viewSearch,
		menu:           []string{"Search", "Volumes", "Re-index", "Config"},
		searchInput:    searchInput,
		addRootInput:   addRootInput,
		availableRoots: availableRoots(),
		cfgInput:       cfgInput,
		theme:          themeByName(cfg.Theme),
		themes:         []string{"tokyonight", "catppuccin", "groovbox"},
		status:         "ready",
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
		case viewVolumes:
			return m.updateVolumes(msg)
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
		case "Volumes":
			m.mode = viewVolumes
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

func (m model) updateVolumes(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if len(m.availableRoots) == 0 {
		return m, nil
	}
	switch msg.String() {
	case "j", "down":
		m.volCursor = min(len(m.availableRoots)-1, m.volCursor+1)
	case "k", "up":
		m.volCursor = max(0, m.volCursor-1)
	case " ", "enter":
		root := m.availableRoots[m.volCursor]
		if slices.Contains(m.cfg.Roots, root) {
			next := make([]string, 0, len(m.cfg.Roots))
			for _, r := range m.cfg.Roots {
				if r != root {
					next = append(next, r)
				}
			}
			m.cfg.Roots = next
		} else {
			m.cfg.Roots = append(m.cfg.Roots, root)
			slices.Sort(m.cfg.Roots)
		}
		if len(m.cfg.Roots) == 0 {
			m.cfg.Roots = []string{"~"}
		}
		if err := config.Save(m.cfg); err != nil {
			m.err = err
		} else {
			m.status = "saved roots"
		}
	}
	return m, nil
}

func (m model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cfgInputActive {
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(m.cfgInput.Value())
			if value != "" && !slices.Contains(m.cfg.Excludes, value) {
				m.cfg.Excludes = append(m.cfg.Excludes, value)
				slices.Sort(m.cfg.Excludes)
				if err := config.Save(m.cfg); err != nil {
					m.err = err
				}
				m.status = "exclude added"
			}
			m.cfgInput.SetValue("")
			m.cfgInputActive = false
			m.cfgInput.Blur()
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

	maxCursor := 2 + len(m.cfg.Excludes)
	switch msg.String() {
	case "j", "down":
		m.cfgCursor = min(maxCursor, m.cfgCursor+1)
	case "k", "up":
		m.cfgCursor = max(0, m.cfgCursor-1)
	case "a":
		m.cfgInputActive = true
		return m, m.cfgInput.Focus()
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
	case "t":
		if m.cfgCursor == 2 {
			idx := slices.Index(m.themes, m.cfg.Theme)
			idx = (idx + 1) % len(m.themes)
			m.cfg.Theme = m.themes[idx]
			m.theme = themeByName(m.cfg.Theme)
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "theme updated"
			}
		}
	case "m":
		if m.cfgCursor == 0 {
			if m.cfg.DefaultScanMode == config.ScanModeHome {
				m.cfg.DefaultScanMode = config.ScanModeConfigured
			} else {
				m.cfg.DefaultScanMode = config.ScanModeHome
			}
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "scan mode updated"
			}
		}
	case "s":
		if m.cfgCursor == 1 {
			m.cfg.AutoScanOnStart = !m.cfg.AutoScanOnStart
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "auto-scan updated"
			}
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
	case viewVolumes:
		b.WriteString(m.viewVolumes())
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
	line := fmt.Sprintf(" indexed=%d  roots=%d  last_scan=%d files in %s  theme=%s",
		m.totalIndexed, len(m.cfg.Roots), m.lastMetrics.Indexed, m.lastMetrics.Elapsed, m.cfg.Theme)
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

func (m model) viewVolumes() string {
	var lines []string
	lines = append(lines, m.theme.Title.Render("Volumes (toggle with enter/space)"))
	for i, root := range m.availableRoots {
		on := slices.Contains(m.cfg.Roots, root)
		check := "[ ]"
		if on {
			check = "[x]"
		}
		prefix := "  "
		style := m.theme.Text
		if i == m.volCursor {
			prefix = "➜ "
			style = m.theme.Highlight
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s %s", prefix, check, root)))
	}
	return strings.Join(lines, "\n")
}

func (m model) viewConfig() string {
	var lines []string
	lines = append(lines, m.theme.Title.Render("Config"))
	prefix := func(idx int) string {
		if m.cfgCursor == idx {
			return "➜ "
		}
		return "  "
	}
	modeLine := fmt.Sprintf("%sscan mode: %s  (press m to toggle)", prefix(0), m.cfg.DefaultScanMode)
	autoLine := fmt.Sprintf("%sauto scan on start: %t  (press s to toggle)", prefix(1), m.cfg.AutoScanOnStart)
	themeLine := fmt.Sprintf("%stheme: %s  (press t to toggle)", prefix(2), m.cfg.Theme)
	lines = append(lines, m.theme.Highlight.Render(modeLine))
	lines = append(lines, m.theme.Highlight.Render(autoLine))
	lines = append(lines, m.theme.Highlight.Render(themeLine))
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
	base := make([]string, 0, len(m.cfg.Roots))
	for _, root := range m.cfg.Roots {
		base = append(base, root)
	}
	if len(base) == 0 {
		base = append(base, "~")
	}
	if len(m.tempRoots) == 0 {
		return strings.Join(base, ", ")
	}
	return strings.Join(base, ", ") + " + temp(" + strings.Join(m.tempRoots, ", ") + ")"
}

func defaultScanRoots(cfg config.Config) ([]string, error) {
	if cfg.DefaultScanMode == config.ScanModeConfigured {
		return config.ResolveRoots(cfg.Roots)
	}
	home, err := config.ExpandPath("~")
	if err != nil {
		return nil, err
	}
	return []string{home}, nil
}

func availableRoots() []string {
	roots := scanner.DiscoverRoots()
	if !slices.Contains(roots, "~") {
		return append([]string{"~"}, roots...)
	}
	return roots
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
