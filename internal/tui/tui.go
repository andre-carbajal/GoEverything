package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
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

type modalMode int

const (
	noModal modalMode = iota
	pathModal
	themeModal
	excludeInputModal
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

	cfg    config.Config
	width  int
	height int

	mode       viewMode
	modal      modalMode
	menu       []string
	menuCursor int

	searchInput  textinput.Model
	searchTable  table.Model
	searchSeq    int
	searchRes    []db.Entry
	searchCur    int
	searchInPath bool

	searchListFocus bool

	cfgCursor      int
	cfgInput       textinput.Model
	cfgInputActive bool
	cfgInputTarget string // "scan" | "exclude"
	cfgThemeCursor int
	cfgPathCursor  int
	pathOptions    []string

	theme        theme
	themes       []string
	totalIndexed int64
	lastMetrics  scanner.Metrics

	busy                 bool
	status               string
	err                  error
	scanCancel           context.CancelFunc
	startupScanAttempted bool
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

	m := model{
		ctx:         ctx,
		cfg:         cfg,
		width:       120,
		height:      36,
		mode:        viewMenu,
		modal:       noModal,
		menu:        []string{"Search", "Scan/Re-index", "Config"},
		searchInput: searchInput,
		cfgInput:    cfgInput,
		theme:       themeByName(cfg.Theme),
		themes:      []string{"tokyonight", "catppuccin", "groovbox"},
		pathOptions: availablePathOptions(),
		status:      "ready",
	}
	m.searchTable = newSearchTable(m.theme)
	m.resizeComponents()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(countCmd(m.ctx, m.cfg.DBPath), textinput.Blink)
}

func newSearchTable(th theme) table.Model {
	t := table.New(
		table.WithColumns(searchColumns(80)),
		table.WithRows(nil),
		table.WithHeight(10),
		table.WithWidth(80),
		table.WithFocused(false),
		table.WithStyles(searchTableStyles(th)),
	)
	return t
}

func searchTableStyles(th theme) table.Styles {
	styles := table.DefaultStyles()
	styles.Header = th.Title.Copy().Padding(0, 0)
	styles.Cell = th.Text.Copy().Padding(0, 0)
	styles.Selected = lipgloss.NewStyle().
		Background(th.SelectBG).
		Foreground(th.SelectFG).
		Bold(true)
	return styles
}

func searchColumns(width int) []table.Column {
	width = max(36, width)
	typeW := 5
	sizeW := 10
	gap := 2
	remaining := max(20, width-typeW-sizeW-gap)
	nameW := max(12, min(34, remaining/3))
	dirW := max(12, remaining-nameW)
	return []table.Column{
		{Title: "Type", Width: typeW},
		{Title: "Name", Width: nameW},
		{Title: "Directory", Width: dirW},
		{Title: "Size", Width: sizeW},
	}
}

func (m model) screenSize() (int, int) {
	return max(40, m.width), max(14, m.height)
}

func (m model) bodySize() (int, int) {
	w, h := m.screenSize()
	return max(24, w-4), max(8, h-2)
}

func (m model) contentHeight() int {
	_, bodyH := m.bodySize()
	headerH := lipgloss.Height(m.renderTopBar())
	if m.mode == viewMenu {
		headerH += lipgloss.Height(m.renderLogo()) + 1
	}
	footerH := lipgloss.Height(m.renderStatus()) + lipgloss.Height(m.renderHelp()) + 1
	return max(3, bodyH-headerH-footerH-2)
}

func (m *model) resizeComponents() {
	bodyW, _ := m.bodySize()
	tableW := max(28, bodyW-6)
	tableH := max(5, m.contentHeight()-5)

	m.searchInput.Width = min(72, max(24, bodyW-28))
	m.cfgInput.Width = max(24, min(80, bodyW-12))
	m.searchTable.SetColumns(searchColumns(tableW))
	m.searchTable.SetWidth(tableW)
	m.searchTable.SetHeight(tableH)
	m.searchTable.SetStyles(searchTableStyles(m.theme))
}

func (m *model) syncSearchTableRows() {
	rows := make([]table.Row, 0, len(m.searchRes))
	for _, entry := range m.searchRes {
		kind := "file"
		if entry.IsDir {
			kind = "dir"
		}
		sizeText := "-"
		if !entry.IsDir {
			sizeText = fmt.Sprintf("%.1f KB", float64(entry.Size)/1024)
		}
		rows = append(rows, table.Row{
			kind,
			entry.Name,
			filepath.Dir(entry.Path),
			sizeText,
		})
	}
	m.searchTable.SetRows(rows)
	if len(rows) == 0 {
		m.searchCur = 0
		m.searchTable.SetCursor(0)
		return
	}
	if m.searchCur >= len(rows) {
		m.searchCur = len(rows) - 1
	}
	m.searchTable.SetCursor(m.searchCur)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeComponents()
		return m, nil

	case countDoneMsg:
		if msg.err == nil {
			m.totalIndexed = msg.total
		}
		if msg.err == nil && !m.busy && !m.startupScanAttempted {
			roots, err := defaultScanRoots(m.cfg)
			if err != nil {
				m.err = err
				return m, nil
			}
			m.startupScanAttempted = true
			m.status = "scanning"
			return m.startScanCmd(roots, "initial-scan", false)
		}
		return m, nil

	case searchDoneMsg:
		if msg.query == m.searchInput.Value() {
			m.searchRes = msg.results
			if m.searchCur >= len(m.searchRes) {
				m.searchCur = max(0, len(m.searchRes)-1)
			}
			m.syncSearchTableRows()
		}
		m.err = msg.err
		return m, nil

	case reindexDoneMsg:
		m.scanCancel = nil
		m.busy = false
		m.lastMetrics = msg.metrics
		if errors.Is(msg.err, context.Canceled) {
			m.err = nil
			m.status = "scan canceled"
			return m, countCmd(m.ctx, m.cfg.DBPath)
		}
		m.err = msg.err
		if msg.err == nil {
			m.status = fmt.Sprintf("re-index done: scanned=%d indexed=%d", msg.metrics.Scanned, msg.metrics.Indexed)
		} else {
			m.status = "re-index failed"
		}
		return m, countCmd(m.ctx, m.cfg.DBPath)

	case scanDoneMsg:
		m.scanCancel = nil
		m.busy = false
		m.lastMetrics = msg.metrics
		if errors.Is(msg.err, context.Canceled) {
			m.err = nil
			m.status = "scan canceled"
			return m, countCmd(m.ctx, m.cfg.DBPath)
		}
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
		return m, searchCmd(m.ctx, m.cfg.DBPath, msg.query, m.searchInPath)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
		if m.modal != noModal {
			return m.handleModalKey(msg)
		}
		switch msg.String() {
		case "ctrl+x":
			if m.busy && m.scanCancel != nil {
				m.status = "stopping scan..."
				m.scanCancel()
				return m, nil
			}
		case "ctrl+g":
			if !m.busy {
				roots, err := defaultScanRoots(m.cfg)
				if err != nil {
					m.err = err
					return m, nil
				}
				m.status = "manual scan in progress…"
				return m.startScanCmd(roots, "manual-scan", false)
			}
		case "ctrl+p":
			if m.mode == viewSearch {
				m.searchInPath = !m.searchInPath
				m.searchSeq++
				return m, debounceCmd(m.searchSeq, m.searchInput.Value())
			}
		case "esc":
			if m.mode != viewMenu {
				m.mode = viewMenu
				return m, nil
			}
		case "/":
			if m.mode != viewSearch {
				m.mode = viewSearch
			}
			m.searchListFocus = false
			m.searchInput.Focus()
			m.searchTable.Blur()
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
			m.searchTable.Blur()
			return m, nil
		case "Scan/Re-index":
			if m.busy {
				return m, nil
			}
			roots, err := defaultScanRoots(m.cfg)
			if err != nil {
				m.err = err
				m.status = "re-index failed"
				return m, nil
			}
			m.status = "re-indexing…"
			return m.startScanCmd(roots, "reindex", true)
		case "Config":
			m.mode = viewConfig
			return m, nil
		}
	}
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "tab" {
		m.searchListFocus = !m.searchListFocus
		if m.searchListFocus {
			m.syncSearchTableRows()
			m.searchInput.Blur()
			m.searchTable.Focus()
		} else {
			m.searchInput.Focus()
			m.searchTable.Blur()
		}
		return m, nil
	}

	if m.searchListFocus {
		switch msg.String() {
		case "enter":
			if len(m.searchRes) > 0 {
				m.searchCur = m.searchTable.Cursor()
				path := m.searchRes[m.searchCur].Path
				return m, openCmd(path, true)
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.searchTable, cmd = m.searchTable.Update(msg)
		m.searchCur = max(0, m.searchTable.Cursor())
		return m, cmd
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchSeq++
	q := m.searchInput.Value()
	return m, tea.Batch(cmd, debounceCmd(m.searchSeq, q))
}

func (m model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalRows := 2 + len(m.cfg.Excludes) // 0 scan,1 theme,2.. excludes
	maxCursor := totalRows - 1
	switch msg.String() {
	case "j", "down":
		m.cfgCursor = min(maxCursor, m.cfgCursor+1)
	case "k", "up":
		m.cfgCursor = max(0, m.cfgCursor-1)
	case "a":
		m.modal = excludeInputModal
		m.cfgInputActive = true
		m.cfgInputTarget = "exclude"
		m.cfgInput.Placeholder = "Add exclude (example: .git or Library/Caches/*)"
		m.cfgInput.Prompt = "exclude> "
		m.cfgInput.SetValue("")
		return m, m.cfgInput.Focus()
	case "enter":
		switch m.cfgCursor {
		case 0:
			m.modal = pathModal
			m.cfgPathCursor = 0
		case 1:
			m.modal = themeModal
			idx := slices.Index(m.themes, m.cfg.Theme)
			if idx < 0 {
				idx = 0
			}
			m.cfgThemeCursor = idx
		}
	case "d":
		exIdx := m.cfgCursor - 2
		if exIdx < 0 || exIdx >= len(m.cfg.Excludes) {
			return m, nil
		}
		m.cfg.Excludes = append(m.cfg.Excludes[:exIdx], m.cfg.Excludes[exIdx+1:]...)
		m.cfgCursor = min(m.cfgCursor, 1+max(0, len(m.cfg.Excludes)))
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

func (m model) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.modal {
	case pathModal:
		return m.handlePathModalKey(msg)
	case themeModal:
		return m.handleThemeModalKey(msg)
	case excludeInputModal:
		return m.handleExcludeInputModalKey(msg)
	default:
		return m, nil
	}
}

func (m model) closeModal() model {
	m.modal = noModal
	m.cfgInputActive = false
	m.cfgInputTarget = ""
	m.cfgInput.Blur()
	m.cfgInput.SetValue("")
	return m
}

func (m model) handlePathModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cfgInputActive {
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(m.cfgInput.Value())
			if value == "" {
				return m.closeModal(), nil
			}
			next, err := m.saveScanPath(value)
			if err != nil {
				next.err = err
				return next, nil
			}
			return next.closeModal(), nil
		case "esc":
			return m.closeModal(), nil
		}
		var cmd tea.Cmd
		m.cfgInput, cmd = m.cfgInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "j", "down":
		m.cfgPathCursor = min(len(m.pathOptions)-1, m.cfgPathCursor+1)
	case "k", "up":
		m.cfgPathCursor = max(0, m.cfgPathCursor-1)
	case "enter":
		choice := m.pathOptions[m.cfgPathCursor]
		if choice == "__custom__" {
			m.cfgInputActive = true
			m.cfgInputTarget = "scan"
			m.cfgInput.SetValue("")
			m.cfgInput.Placeholder = "Custom scan path (example: ~/Projects)"
			m.cfgInput.Prompt = "scan-path> "
			return m, m.cfgInput.Focus()
		}
		next, err := m.saveScanPath(choice)
		if err != nil {
			next.err = err
			return next, nil
		}
		return next.closeModal(), nil
	case "esc":
		return m.closeModal(), nil
	}
	return m, nil
}

func (m model) handleThemeModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.cfgThemeCursor = min(len(m.themes)-1, m.cfgThemeCursor+1)
	case "k", "up":
		m.cfgThemeCursor = max(0, m.cfgThemeCursor-1)
	case "enter":
		m.cfg.Theme = m.themes[m.cfgThemeCursor]
		m.theme = themeByName(m.cfg.Theme)
		m.resizeComponents()
		if err := config.Save(m.cfg); err != nil {
			m.err = err
		} else {
			m.status = "theme updated"
		}
		return m.closeModal(), nil
	case "esc":
		return m.closeModal(), nil
	}
	return m, nil
}

func (m model) handleExcludeInputModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		value := strings.TrimSpace(m.cfgInput.Value())
		if value != "" && !slices.Contains(m.cfg.Excludes, value) {
			m.cfg.Excludes = append(m.cfg.Excludes, value)
			slices.Sort(m.cfg.Excludes)
			if err := config.Save(m.cfg); err != nil {
				m.err = err
			} else {
				m.status = "exclude added"
			}
		}
		return m.closeModal(), nil
	case "esc":
		return m.closeModal(), nil
	}
	var cmd tea.Cmd
	m.cfgInput, cmd = m.cfgInput.Update(msg)
	return m, cmd
}

func (m model) saveScanPath(value string) (model, error) {
	root, err := config.ExpandPath(value)
	if err != nil {
		return m, err
	}
	info, statErr := os.Stat(root)
	if statErr != nil {
		return m, statErr
	}
	if !info.IsDir() {
		return m, fmt.Errorf("%s is not a directory", root)
	}
	m.cfg.DefaultScanPath = value
	if err := config.Save(m.cfg); err != nil {
		return m, err
	}
	m.status = "saved scan location"
	return m, nil
}

func (m model) View() string {
	m.resizeComponents()
	return m.renderFrame()
}

func (m model) renderFrame() string {
	screenW, screenH := m.screenSize()
	bodyW, bodyH := m.bodySize()

	parts := make([]string, 0, 5)
	if m.mode == viewMenu {
		parts = append(parts, m.renderLogo())
	}
	parts = append(parts, m.renderTopBar())

	status := m.renderStatus()
	help := m.renderHelp()
	contentH := m.contentHeight()
	content := m.renderContent(bodyW, contentH)
	if m.modal != noModal {
		content = m.renderModal(bodyW, contentH)
	}
	parts = append(parts, content, status, help)

	body := lipgloss.JoinVertical(lipgloss.Left, parts...)
	body = lipgloss.NewStyle().Width(bodyW).Height(bodyH).Render(body)

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(0, 1).
		Width(screenW - 2).
		Height(screenH - 2).
		Render(body)
}

func (m model) renderContent(width, height int) string {
	var content string
	switch m.mode {
	case viewMenu:
		content = m.viewMenu(width)
	case viewSearch:
		content = m.viewSearch(width, height)
	case viewConfig:
		content = m.viewConfig(width, height)
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, content)
}

func (m model) renderStatus() string {
	maxWidth := max(20, m.width-6)
	if m.err != nil {
		return m.theme.Err.Render(trimMiddle("error: "+m.err.Error(), maxWidth))
	}
	status := "status: " + m.status
	if m.busy {
		status = "status: scanning"
	}
	return m.theme.Muted.Render(trimMiddle(status, maxWidth))
}

func (m model) renderHelp() string {
	keys := "j/k move • enter select • ctrl+g scan now • ctrl+x stop scan • esc back • q quit"
	if m.modal != noModal {
		keys = "j/k move • enter select • esc close modal • q quit"
	} else if m.mode == viewConfig {
		keys = "j/k move • enter select/edit • a add exclude • d remove exclude • ctrl+g scan now • esc back • q quit"
	} else if m.mode == viewSearch {
		keys = "tab focus input/list • j/k move list • enter open folder • / focus search • ctrl+p toggle path filter • ctrl+g scan now • ctrl+x stop scan • esc back • q quit"
	}
	return m.theme.Muted.Render(trimMiddle("keys: "+keys, max(20, m.width-6)))
}

func (m model) renderLogo() string {
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
	asciiBlock := strings.Join(ascii, "\n")
	asciiWidth := lipgloss.Width(asciiBlock)
	available, _ := m.bodySize()

	logo := m.theme.Header.Render(asciiBlock)
	if asciiWidth > available {
		compactLabel := "GOEVERYTHING"
		if available < 34 {
			compactLabel = "GE"
		}
		logo = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.Border).
			Padding(0, 1).
			Render(m.theme.Header.Render(compactLabel))
	}
	return logo
}

func (m model) renderTopBar() string {
	bodyW, _ := m.bodySize()
	innerWidth := max(24, min(bodyW, 120))

	scopeLen := max(8, innerWidth/4)
	basePlain := fmt.Sprintf("◌ GoEverything %d indexed scope %s", m.totalIndexed, trimMiddle(m.cfg.DefaultScanPath, scopeLen))
	lastPlain := "last scan " + prettyElapsed(m.lastMetrics.Elapsed)
	statusPlain := "idle"
	if m.busy {
		statusPlain = "scanning"
	}

	lastView := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Badge).
		Padding(0, 1).
		Render(m.theme.Muted.Render(lastPlain))
	statusStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(0, 1).
		Render(m.theme.Muted.Render(statusPlain))
	if m.busy {
		statusText := m.theme.Highlight.Copy().Bold(true).Render("● " + statusPlain)
		statusStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.BorderHi).
			Padding(0, 1).
			Render(statusText)
	}

	mainView := m.theme.Header.Render(basePlain)
	content := lipgloss.JoinHorizontal(lipgloss.Center, mainView, "  ", lastView, "  ", statusStyle)
	if lipgloss.Width(content) > innerWidth-2 {
		shortBase := trimMiddle(basePlain, max(12, innerWidth/3))
		mainView = m.theme.Header.Render(shortBase)
		content = lipgloss.JoinHorizontal(lipgloss.Center, mainView, "  ", statusStyle)
	}
	if lipgloss.Width(content) > innerWidth-2 {
		content = m.theme.Header.Render(trimMiddle(basePlain, max(10, innerWidth-4)))
	}

	bar := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(0, 1).
		Width(innerWidth - 2).
		Render(content)
	return bar
}

func (m model) panelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(0, 1)
}

func (m model) itemStyle(active bool) lipgloss.Style {
	st := m.panelStyle()
	if active {
		st = st.BorderForeground(m.theme.BorderHi).Background(m.theme.SurfaceBG)
	}
	return st
}

func (m model) badgeStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Badge).
		Padding(0, 1)
}

func (m model) inputFocusStyle() lipgloss.Style {
	return lipgloss.NewStyle().Background(m.theme.InputBG).Foreground(m.theme.InputFG)
}

func (m model) viewMenu(width int) string {
	type menuCard struct {
		Title  string
		Desc   string
		Hotkey string
	}
	cards := []menuCard{
		{Title: "Search", Desc: "Find files and folders across your index", Hotkey: "/"},
		{Title: "Scan / Re-index", Desc: "Rebuild the file index from scratch", Hotkey: "Ctrl+G"},
		{Title: "Config", Desc: "Paths, themes and scan preferences", Hotkey: "C"},
	}

	renderCard := func(card menuCard, active bool) string {
		base := m.itemStyle(active).Width(max(28, min(54, width-6)))

		title := m.theme.Text.Copy().Bold(true).Render(card.Title)
		desc := m.theme.Muted.Render(card.Desc)
		hotkey := m.badgeStyle().Render(card.Hotkey)
		body := lipgloss.JoinHorizontal(
			lipgloss.Top,
			lipgloss.JoinVertical(lipgloss.Left, title, desc),
			lipgloss.NewStyle().Width(4).Render(""),
			hotkey,
		)
		return base.Render(body)
	}

	lines := []string{m.theme.Title.Render("MAIN MENU")}
	for i, card := range cards {
		lines = append(lines, renderCard(card, i == m.menuCursor))
	}
	return m.panelStyle().Width(max(32, min(60, width-4))).Render(strings.Join(lines, "\n\n"))
}

func (m model) viewSearch(width, height int) string {
	var lines []string
	chip := func(s string, active bool) string {
		st := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.Badge).
			Foreground(m.theme.Muted.GetForeground()).
			Padding(0, 1)
		if active {
			st = st.BorderForeground(m.theme.BorderHi).Foreground(m.theme.Text.GetForeground())
		}
		return st.Render(s)
	}
	top := lipgloss.JoinHorizontal(lipgloss.Center,
		m.theme.Highlight.Render("search"),
		m.theme.Muted.Render(" in "),
		chip(m.cfg.DefaultScanPath, true),
	)
	if m.searchInPath {
		top = lipgloss.JoinHorizontal(lipgloss.Center, top, " ", chip("path-filter", true))
	}
	lines = append(lines, top)

	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Input).
		Padding(0, 1)
	inputText := m.searchInput.View()
	if !m.searchListFocus {
		inputText = m.inputFocusStyle().Render(inputText)
	}
	boxLine := lipgloss.JoinHorizontal(lipgloss.Top, "🔎 ", inputText, "   ", m.theme.Muted.Render(fmt.Sprintf("%d results", len(m.searchRes))))
	lines = append(lines, searchBox.Render(boxLine))

	if len(m.searchRes) == 0 {
		lines = append(lines, m.theme.Muted.Render("No results"))
	} else {
		tableTitle := m.theme.Title.Render("RESULTS")
		if m.searchListFocus {
			tableTitle = m.theme.Highlight.Render("RESULTS")
		}
		lines = append(lines, tableTitle)
		lines = append(lines, m.searchTable.View())
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Border).
		Padding(0, 1).
		Width(max(24, width-2)).
		Height(max(3, height-2)).
		Render(strings.Join(lines, "\n"))
	return card
}

func (m model) renderModal(width, height int) string {
	var modal string
	switch m.modal {
	case pathModal:
		modal = m.renderPathModal(width)
	case themeModal:
		modal = m.renderThemeModal(width)
	case excludeInputModal:
		modal = m.renderExcludeInputModal(width)
	default:
		return m.renderContent(width, height)
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

func (m model) modalStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.BorderHi).
		Padding(1, 2).
		Width(max(28, min(72, width-8)))
}

func (m model) renderPathModal(width int) string {
	lines := []string{
		m.theme.Title.Render("SELECT LOCATION"),
		m.theme.Muted.Render("j/k move • enter select • esc cancel"),
	}
	if m.cfgInputActive {
		lines = append(lines,
			"",
			m.theme.Muted.Render("Custom scan path"),
			lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(m.theme.Input).
				Padding(0, 1).
				Render(m.cfgInput.View()),
		)
		return m.modalStyle(width).Render(strings.Join(lines, "\n"))
	}
	for i, p := range m.pathOptions {
		label := p
		if p == "__custom__" {
			label = "Custom path..."
		}
		prefix := "  "
		st := m.theme.Text
		if i == m.cfgPathCursor {
			prefix = "➜ "
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
		}
		lines = append(lines, st.Render(prefix+trimMiddle(label, max(12, min(64, width-16)))))
	}
	return m.modalStyle(width).Render(strings.Join(lines, "\n"))
}

func (m model) renderThemeModal(width int) string {
	lines := []string{
		m.theme.Title.Render("SELECT THEME"),
		m.theme.Muted.Render("j/k move • enter select • esc cancel"),
		"",
	}
	for i, th := range m.themes {
		prefix := "  "
		st := m.theme.Text
		if i == m.cfgThemeCursor {
			prefix = "➜ "
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
		}
		lines = append(lines, st.Render(prefix+th))
	}
	return m.modalStyle(width).Render(strings.Join(lines, "\n"))
}

func (m model) renderExcludeInputModal(width int) string {
	lines := []string{
		m.theme.Title.Render("ADD EXCLUDE PATTERN"),
		m.theme.Muted.Render("enter save • esc cancel"),
		"",
		lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.Input).
			Padding(0, 1).
			Render(m.cfgInput.View()),
	}
	return m.modalStyle(width).Render(strings.Join(lines, "\n"))
}

func prettyElapsed(d time.Duration) string {
	if d <= 0 {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

func trimMiddle(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	left := (maxLen - 1) / 2
	right := maxLen - left - 1
	return s[:left] + "…" + s[len(s)-right:]
}

func (m model) viewConfig(width, height int) string {
	outerWidth := min(120, max(44, width-2))
	wide := outerWidth >= 96
	gap := 2
	leftW := outerWidth
	rightW := 0
	if wide {
		leftW = int(float64(outerWidth) * 0.56)
		leftW = max(30, leftW)
		rightW = outerWidth - leftW - gap
		if rightW < 24 {
			wide = false
			leftW = outerWidth
		}
	}
	leftInnerW := max(24, leftW-4)
	rightInnerW := max(22, rightW-4)

	configRow := func(idx int, title, value, action string, width int) string {
		val := trimMiddle(value, max(12, width-4))
		body := title + "\n" + m.theme.Text.Render(val)
		if action != "" {
			body += "\n" + m.theme.Muted.Render(action)
		}
		return m.itemStyle(m.cfgCursor == idx).Width(width).Render(body)
	}

	leftRows := []string{
		m.theme.Title.Render("SETTINGS"),
		configRow(0, "scan location", m.cfg.DefaultScanPath, "↵ change", leftInnerW),
		configRow(1, "theme", m.cfg.Theme, "↵ select", leftInnerW),
	}
	leftPanel := strings.Join(leftRows, "\n\n")

	rightRows := []string{m.theme.Title.Render("EXCLUDE PATTERNS")}
	var exLines []string
	for i, ex := range m.cfg.Excludes {
		idx := i + 2
		row := "◌ " + trimMiddle(ex, max(12, rightInnerW-6))
		st := m.theme.Text
		if m.cfgCursor == idx {
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
		}
		exLines = append(exLines, st.Render(row))
	}
	rightRows = append(rightRows, m.panelStyle().Width(rightInnerW).Render(strings.Join(exLines, "\n")))
	rightRows = append(rightRows, m.theme.Muted.Render("A add pattern • D remove selected"))
	rightPanel := strings.Join(rightRows, "\n")

	if !wide {
		stacked := m.panelStyle().Width(outerWidth).Height(max(3, height-2)).Render(leftPanel + "\n\n" + rightPanel)
		return stacked
	}

	layout := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(leftW).Render(leftPanel),
		lipgloss.NewStyle().Width(gap).Render(""),
		lipgloss.NewStyle().Width(rightW).Render(rightPanel),
	)
	return m.panelStyle().Width(outerWidth).Height(max(3, height-2)).Render(layout)
}

func searchCmd(ctx context.Context, dbPath, query string, searchInPath bool) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, dbPath)
		if err != nil {
			return searchDoneMsg{query: query, err: err}
		}
		defer store.Close()
		res, err := store.SearchAdvanced(ctx, db.SearchOptions{
			Query:        query,
			SearchInPath: searchInPath,
			Limit:        100,
			Offset:       0,
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

func (m model) startScanCmd(roots []string, label string, reindex bool) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.scanCancel = cancel
	m.busy = true
	if reindex {
		return m, reindexCmd(ctx, m.cfg, roots)
	}
	return m, scanRootsCmd(ctx, m.cfg, roots, label)
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
	return m.cfg.DefaultScanPath
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
	Border    lipgloss.Color
	BorderHi  lipgloss.Color
	SurfaceBG lipgloss.Color
	Badge     lipgloss.Color
	Input     lipgloss.Color
	InputBG   lipgloss.Color
	InputFG   lipgloss.Color
	SelectBG  lipgloss.Color
	SelectFG  lipgloss.Color
	BusyFG    lipgloss.Color
	BusyBG    lipgloss.Color
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
			Border:    lipgloss.Color("#504945"),
			BorderHi:  lipgloss.Color("#83a598"),
			SurfaceBG: lipgloss.Color("#282828"),
			Badge:     lipgloss.Color("#665c54"),
			Input:     lipgloss.Color("#83a598"),
			InputBG:   lipgloss.Color("#3c3836"),
			InputFG:   lipgloss.Color("#ebdbb2"),
			SelectBG:  lipgloss.Color("#3c3836"),
			SelectFG:  lipgloss.Color("#fbf1c7"),
			BusyFG:    lipgloss.Color("#b8bb26"),
			BusyBG:    lipgloss.Color("#3c3836"),
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
			Border:    lipgloss.Color("#45475a"),
			BorderHi:  lipgloss.Color("#89dceb"),
			SurfaceBG: lipgloss.Color("#1e1e2e"),
			Badge:     lipgloss.Color("#585b70"),
			Input:     lipgloss.Color("#89dceb"),
			InputBG:   lipgloss.Color("#313244"),
			InputFG:   lipgloss.Color("#f5e0dc"),
			SelectBG:  lipgloss.Color("#313244"),
			SelectFG:  lipgloss.Color("#f5e0dc"),
			BusyFG:    lipgloss.Color("#a6e3a1"),
			BusyBG:    lipgloss.Color("#313244"),
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
			Border:    lipgloss.Color("#2d3655"),
			BorderHi:  lipgloss.Color("#5a86ff"),
			SurfaceBG: lipgloss.Color("#1b2138"),
			Badge:     lipgloss.Color("#3a4668"),
			Input:     lipgloss.Color("#356cff"),
			InputBG:   lipgloss.Color("#2f3b63"),
			InputFG:   lipgloss.Color("#ffffff"),
			SelectBG:  lipgloss.Color("#2f3b63"),
			SelectFG:  lipgloss.Color("#ffffff"),
			BusyFG:    lipgloss.Color("#9ece6a"),
			BusyBG:    lipgloss.Color("#1f2335"),
		}
	}
}
