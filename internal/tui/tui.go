package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	viewStartup viewMode = iota
	viewSearch
	viewConfig
)

type modalMode int

const (
	noModal modalMode = iota
	pathModal
	themeModal
	excludeInputModal
	deleteConfirmModal
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

type scanProgressTickMsg struct {
	session int
}

type deleteResultDoneMsg struct {
	index int
	entry db.Entry
	total int64
	err   error
}

type hitbox struct {
	x int
	y int
	w int
	h int
}

type mouseTargetKind int

const (
	mouseTargetNone mouseTargetKind = iota
	mouseTargetSettings
	mouseTargetSearchInput
	mouseTargetSearchResult
	mouseTargetConfigRow
	mouseTargetConfigExclude
	mouseTargetPathOption
	mouseTargetThemeOption
	mouseTargetModalInput
	mouseTargetModalOutside
)

type mouseTarget struct {
	kind  mouseTargetKind
	index int
}

type searchMouseLayout struct {
	settingsButton hitbox
	searchInput    hitbox
	firstResultY   int
	resultX        int
	resultW        int
	visibleStart   int
	visibleEnd     int
}

type scanProgressSource struct {
	mu       sync.Mutex
	session  int
	progress scanner.Progress
}

func newScanProgressSource() *scanProgressSource {
	return &scanProgressSource{}
}

func (s *scanProgressSource) reset(session int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = session
	s.progress = scanner.Progress{}
}

func (s *scanProgressSource) update(session int, progress scanner.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != session {
		return
	}
	s.progress = progress
}

func (s *scanProgressSource) snapshot(session int) (scanner.Progress, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != session {
		return scanner.Progress{}, false
	}
	return s.progress, true
}

type model struct {
	ctx context.Context

	cfg    config.Config
	width  int
	height int

	mode  viewMode
	modal modalMode

	searchInput textinput.Model
	searchTable table.Model
	searchSeq   int
	searchRes   []db.Entry
	searchCur   int

	deleteIndex     int
	deleteTarget    db.Entry
	hasDeleteTarget bool

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
	scanProgress         scanner.Progress
	scanSession          int
	scanProgressSource   *scanProgressSource
	activeScanLabel      string

	hoveredMouse   mouseTarget
	pressedMouse   mouseTarget
	dragOrigin     mouseTarget
	lastClickMouse mouseTarget
	lastClickAt    time.Time
}

func Run(ctx context.Context, cfg config.Config) error {
	m := newModel(ctx, cfg)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())
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
		mode:        viewStartup,
		modal:       noModal,
		searchInput: searchInput,
		cfgInput:    cfgInput,
		theme:       themeByName(cfg.Theme),
		themes:      []string{"tokyonight", "catppuccin", "groovbox"},
		pathOptions: availablePathOptions(),
		status:      "preparing initial scan",

		scanProgressSource: newScanProgressSource(),
	}
	m.searchTable = newSearchTable(m.theme)
	m = m.resizeComponents()
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
	styles.Header = th.Title.Padding(0, 0)
	styles.Cell = th.Text.Padding(0, 0)
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
	footerH := lipgloss.Height(m.renderError()) + lipgloss.Height(m.renderHelp()) + 1
	return max(3, bodyH-headerH-footerH-2)
}

func (m model) resizeComponents() model {
	bodyW, _ := m.bodySize()
	tableW := max(28, bodyW-6)
	tableH := max(5, m.contentHeight()-5)

	m.searchInput.Width = min(72, max(24, bodyW-28))
	m.cfgInput.Width = max(24, min(80, bodyW-12))
	m.searchTable.SetColumns(searchColumns(tableW))
	m.searchTable.SetWidth(tableW)
	m.searchTable.SetHeight(tableH)
	m.searchTable.SetStyles(searchTableStyles(m.theme))
	return m
}

func (m model) syncSearchTableRows() model {
	rows := make([]table.Row, 0, len(m.searchRes))
	for _, entry := range m.searchRes {
		rows = append(rows, table.Row(searchEntryValues(entry)))
	}
	m.searchTable.SetRows(rows)
	if len(rows) == 0 {
		m.searchCur = 0
		m.searchTable.SetCursor(0)
		return m
	}
	if m.searchCur >= len(rows) {
		m.searchCur = len(rows) - 1
	}
	m.searchTable.SetCursor(m.searchCur)
	return m
}

func (m model) focusSearchView() model {
	m.mode = viewSearch
	m.searchInput.Focus()
	m.searchTable.Blur()
	return m
}

func (m model) openSettingsView() model {
	m.mode = viewConfig
	m.searchInput.Blur()
	m.searchTable.Blur()
	return m
}

func (m model) settingsButtonHitbox() hitbox {
	return m.searchMouseLayout().settingsButton
}

func (h hitbox) contains(x, y int) bool {
	return x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h
}

func (m model) contentStartY() int {
	y := 1
	y += lipgloss.Height(m.renderTopBar())
	return y
}

func (m model) contentStartX() int {
	return 2
}

func (m model) searchInputHitbox() hitbox {
	return m.searchMouseLayout().searchInput
}

func (m model) searchVisibleWindow() (cursor int, start int, end int) {
	if len(m.searchRes) == 0 {
		return 0, 0, 0
	}
	cursor = min(max(0, m.searchTable.Cursor()), len(m.searchRes)-1)
	visibleRows := max(1, m.searchTable.Height())
	if cursor >= visibleRows {
		start = cursor - visibleRows + 1
	}
	end = min(len(m.searchRes), start+visibleRows)
	if end-start < visibleRows {
		start = max(0, end-visibleRows)
	}
	return cursor, start, end
}

func (m model) searchVisibleRange() (int, int) {
	_, start, end := m.searchVisibleWindow()
	return start, end
}

func (m model) searchResultHitbox(index int) hitbox {
	layout := m.searchMouseLayout()
	return hitbox{
		x: layout.resultX,
		y: layout.firstResultY + (index - layout.visibleStart),
		w: layout.resultW,
		h: 1,
	}
}

func (m model) searchTopRow(width int) (string, int) {
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
	settingsButton := m.settingsButtonView()
	topAvailable := max(20, width-8)
	spacerW := max(1, topAvailable-lipgloss.Width(top)-lipgloss.Width(settingsButton))
	settingsOffset := lipgloss.Width(top) + spacerW
	topRow := lipgloss.JoinHorizontal(lipgloss.Center, top, strings.Repeat(" ", spacerW), settingsButton)
	if lipgloss.Width(topRow) > topAvailable {
		shortSearch := m.theme.Highlight.Render("search")
		settingsOffset = lipgloss.Width(shortSearch) + 1
		topRow = lipgloss.JoinHorizontal(lipgloss.Center, shortSearch, " ", settingsButton)
	}
	return topRow, settingsOffset
}

func (m model) searchBoxView() string {
	searchBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Input).
		Padding(0, 1)
	inputText := m.inputFocusStyle().Render(m.searchInput.View())
	boxLine := lipgloss.JoinHorizontal(lipgloss.Top, "🔎 ", inputText, "   ", m.theme.Muted.Render(fmt.Sprintf("%d results", len(m.searchRes))))
	return searchBox.Render(boxLine)
}

func (m model) searchMouseLayout() searchMouseLayout {
	bodyW, _ := m.bodySize()
	contentX := m.contentStartX()
	contentY := m.contentStartY()
	lineX := contentX + 2 // search card border + horizontal padding

	topRow, settingsOffset := m.searchTopRow(bodyW)
	settingsButton := m.settingsButtonView()
	searchBox := m.searchBoxView()
	start, end := m.searchVisibleRange()
	resultW := max(searchColumnsWidth(m.searchTable.Columns()), m.searchTable.Width())

	// These heights are derived from the same rendered fragments that viewSearch
	// composes: card border/padding line, top row, search input box, "RESULTS",
	// and the table header line produced by renderSearchResults.
	firstResultY := contentY + 1 + lipgloss.Height(topRow) + lipgloss.Height(searchBox) + 2

	return searchMouseLayout{
		settingsButton: hitbox{
			x: lineX + settingsOffset,
			y: contentY + 1,
			w: lipgloss.Width(settingsButton),
			h: lipgloss.Height(settingsButton),
		},
		searchInput: hitbox{
			x: lineX,
			y: contentY + 1 + lipgloss.Height(topRow),
			w: lipgloss.Width(searchBox),
			h: lipgloss.Height(searchBox),
		},
		firstResultY: firstResultY,
		resultX:      lineX,
		resultW:      resultW,
		visibleStart: start,
		visibleEnd:   end,
	}
}

func (m model) configRowHitbox(index int) hitbox {
	bodyW, _ := m.bodySize()
	outerWidth := min(120, max(44, bodyW-2))
	wide := outerWidth >= 96
	leftW := outerWidth
	if wide {
		leftW = max(30, int(float64(outerWidth)*0.56))
	}
	return hitbox{
		x: m.contentStartX() + 1,
		y: m.contentStartY() + 3 + index*5,
		w: max(28, leftW-4),
		h: 3,
	}
}

func (m model) configExcludeHitbox(index int) hitbox {
	bodyW, _ := m.bodySize()
	outerWidth := min(120, max(44, bodyW-2))
	wide := outerWidth >= 96
	gap := 2
	x := m.contentStartX() + 1
	y := m.contentStartY() + 20 + index
	w := max(24, outerWidth-4)
	if wide {
		leftW := max(30, int(float64(outerWidth)*0.56))
		rightW := outerWidth - leftW - gap
		if rightW >= 24 {
			x = m.contentStartX() + leftW + gap + 1
			y = m.contentStartY() + 3 + index
			w = max(22, rightW-4)
		}
	}
	return hitbox{x: x, y: y, w: w, h: 1}
}

func (m model) modalBoxHitbox() hitbox {
	bodyW, _ := m.bodySize()
	contentH := m.contentHeight()
	modalW := max(28, min(72, bodyW-8))
	modalH := 8
	switch m.modal {
	case noModal:
	case pathModal:
		modalH = 6 + len(m.pathOptions)
		if m.cfgInputActive {
			modalH = 11
		}
	case themeModal:
		modalH = 7 + len(m.themes)
	case excludeInputModal:
		modalH = 10
	case deleteConfirmModal:
		modalH = 11
	}
	return hitbox{
		x: m.contentStartX() + max(0, (bodyW-modalW)/2),
		y: m.contentStartY() + max(0, (contentH-modalH)/2),
		w: modalW,
		h: modalH,
	}
}

func (m model) pathOptionHitbox(index int) hitbox {
	box := m.modalBoxHitbox()
	return hitbox{x: box.x + 2, y: box.y + 4 + index, w: max(1, box.w-4), h: 1}
}

func (m model) themeOptionHitbox(index int) hitbox {
	box := m.modalBoxHitbox()
	return hitbox{x: box.x + 2, y: box.y + 5 + index, w: max(1, box.w-4), h: 1}
}

func (m model) modalInputHitbox() hitbox {
	box := m.modalBoxHitbox()
	return hitbox{x: box.x + 2, y: box.y + box.h - 3, w: max(1, box.w-4), h: 3}
}

func (m model) resolveMouseTarget(x, y int) mouseTarget {
	if m.modal != noModal {
		switch m.modal {
		case noModal:
		case pathModal:
			if m.cfgInputActive {
				if m.modalInputHitbox().contains(x, y) {
					return mouseTarget{kind: mouseTargetModalInput}
				}
			} else {
				for i := range m.pathOptions {
					if m.pathOptionHitbox(i).contains(x, y) {
						return mouseTarget{kind: mouseTargetPathOption, index: i}
					}
				}
			}
		case themeModal:
			for i := range m.themes {
				if m.themeOptionHitbox(i).contains(x, y) {
					return mouseTarget{kind: mouseTargetThemeOption, index: i}
				}
			}
		case excludeInputModal:
			if m.modalInputHitbox().contains(x, y) {
				return mouseTarget{kind: mouseTargetModalInput}
			}
		case deleteConfirmModal:
		}
		if !m.modalBoxHitbox().contains(x, y) {
			return mouseTarget{kind: mouseTargetModalOutside}
		}
		return mouseTarget{kind: mouseTargetNone}
	}

	switch m.mode {
	case viewStartup:
	case viewSearch:
		if m.settingsButtonHitbox().contains(x, y) {
			return mouseTarget{kind: mouseTargetSettings}
		}
		if m.searchInputHitbox().contains(x, y) {
			return mouseTarget{kind: mouseTargetSearchInput}
		}
		start, end := m.searchVisibleRange()
		for i := start; i < end; i++ {
			if m.searchResultHitbox(i).contains(x, y) {
				return mouseTarget{kind: mouseTargetSearchResult, index: i}
			}
		}
	case viewConfig:
		for i := 0; i < 3; i++ {
			if m.configRowHitbox(i).contains(x, y) {
				return mouseTarget{kind: mouseTargetConfigRow, index: i}
			}
		}
		for i := range m.cfg.Excludes {
			if m.configExcludeHitbox(i).contains(x, y) {
				return mouseTarget{kind: mouseTargetConfigExclude, index: i}
			}
		}
	}
	return mouseTarget{kind: mouseTargetNone}
}

func (m model) mouseHoverMatches(kind mouseTargetKind, index int) bool {
	return m.hoveredMouse.kind == kind && m.hoveredMouse.index == index
}

// noinspection GoAssignmentToReceiver
func (m model) selectSearchResult(index int) model {
	if index < 0 || index >= len(m.searchRes) {
		return m
	}
	m.searchCur = index
	m = m.syncSearchTableRows()
	m.searchTable.SetCursor(index)
	m.searchInput.Focus()
	m.searchTable.Blur()
	return m
}

// noinspection GoAssignmentToReceiver
func (m model) setSearchCursor(index int) model {
	if len(m.searchRes) == 0 {
		return m
	}
	m = m.syncSearchTableRows()
	index = min(max(0, index), len(m.searchRes)-1)
	m.searchCur = index
	m.searchTable.SetCursor(index)
	return m
}

// noinspection GoAssignmentToReceiver
func (m model) openSelectedDeleteModal(index int) model {
	if index < 0 || index >= len(m.searchRes) {
		return m
	}
	m = m.selectSearchResult(index)
	m.deleteIndex = index
	m.deleteTarget = m.searchRes[index]
	m.hasDeleteTarget = true
	m.modal = deleteConfirmModal
	return m
}

func (m model) focusSearchInput() model {
	m.searchInput.Focus()
	m.searchTable.Blur()
	return m
}

func (m model) activateConfigRow(index int) (model, tea.Cmd) {
	m.cfgCursor = min(max(0, index), 2+max(0, len(m.cfg.Excludes)))
	switch index {
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
	case 2:
		m.cfg.DeleteMode = nextDeleteMode(m.cfg.DeleteMode)
		if err := config.Save(m.cfg); err != nil {
			m.err = err
		} else {
			m.status = "delete mode updated"
		}
	}
	return m, nil
}

func (m model) removeExcludeAt(index int) model {
	if index < 0 || index >= len(m.cfg.Excludes) {
		return m
	}
	m.cfg.Excludes = append(m.cfg.Excludes[:index], m.cfg.Excludes[index+1:]...)
	m.cfgCursor = min(m.cfgCursor, 2+max(0, len(m.cfg.Excludes)))
	if len(m.cfg.Excludes) == 0 {
		m.cfg.Excludes = scanner.DefaultExcludes()
	}
	if err := config.Save(m.cfg); err != nil {
		m.err = err
	} else {
		m.status = "exclude removed"
	}
	return m
}

func (m model) handlePathOptionClick(index int) (model, tea.Cmd) {
	if index < 0 || index >= len(m.pathOptions) {
		return m, nil
	}
	m.cfgPathCursor = index
	choice := m.pathOptions[index]
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
		m.err = err
		return m, nil
	}
	return next.closeModal(), nil
}

// noinspection GoAssignmentToReceiver
func (m model) handleThemeOptionClick(index int) (model, tea.Cmd) {
	if index < 0 || index >= len(m.themes) {
		return m, nil
	}
	m.cfgThemeCursor = index
	m.cfg.Theme = m.themes[index]
	m.theme = themeByName(m.cfg.Theme)
	m = m.resizeComponents()
	if err := config.Save(m.cfg); err != nil {
		m.err = err
	} else {
		m.status = "theme updated"
	}
	return m.closeModal(), nil
}

// noinspection GoAssignmentToReceiver
func (m model) scrollSearchResults(delta int) model {
	if len(m.searchRes) == 0 {
		return m
	}
	m = m.selectSearchResult(min(max(0, m.searchTable.Cursor()+delta), len(m.searchRes)-1))
	return m
}

func (m model) scrollConfig(delta int) model {
	totalRows := 3 + len(m.cfg.Excludes)
	if totalRows <= 0 {
		return m
	}
	m.cfgCursor = min(max(0, m.cfgCursor+delta), totalRows-1)
	return m
}

func (m model) scrollModal(delta int) model {
	switch m.modal {
	case noModal:
	case pathModal:
		if !m.cfgInputActive && len(m.pathOptions) > 0 {
			m.cfgPathCursor = min(max(0, m.cfgPathCursor+delta), len(m.pathOptions)-1)
		}
	case themeModal:
		if len(m.themes) > 0 {
			m.cfgThemeCursor = min(max(0, m.cfgThemeCursor+delta), len(m.themes)-1)
		}
	case excludeInputModal, deleteConfirmModal:
	}
	return m
}

// noinspection GoAssignmentToReceiver
func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	ev := tea.MouseEvent(msg)
	target := m.resolveMouseTarget(ev.X, ev.Y)

	if ev.Action == tea.MouseActionMotion {
		m.hoveredMouse = target
		if ev.Button == tea.MouseButtonLeft && target.kind == mouseTargetSearchResult {
			m = m.selectSearchResult(target.index)
		}
		return m, nil
	}

	if ev.IsWheel() {
		delta := 0
		switch ev.Button {
		case tea.MouseButtonWheelUp:
			delta = -1
		case tea.MouseButtonWheelDown:
			delta = 1
		case tea.MouseButtonNone, tea.MouseButtonLeft, tea.MouseButtonMiddle, tea.MouseButtonRight,
			tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight, tea.MouseButtonBackward,
			tea.MouseButtonForward, tea.MouseButton10, tea.MouseButton11:
		}
		if delta == 0 {
			return m, nil
		}
		if m.modal != noModal {
			return m.scrollModal(delta), nil
		}
		switch m.mode {
		case viewStartup:
		case viewSearch:
			m = m.scrollSearchResults(delta)
		case viewConfig:
			m = m.scrollConfig(delta)
		}
		return m, nil
	}

	rightClick := ev.Button == tea.MouseButtonRight && (ev.Action == tea.MouseActionPress || ev.Action == tea.MouseActionRelease)
	if rightClick && target.kind == mouseTargetSearchResult {
		return m.openSelectedDeleteModal(target.index), nil
	}
	if rightClick && target.kind == mouseTargetConfigExclude {
		m.cfgCursor = target.index + 3
		return m.removeExcludeAt(target.index), nil
	}

	if ev.Action == tea.MouseActionRelease {
		m.pressedMouse = mouseTarget{kind: mouseTargetNone}
		m.dragOrigin = mouseTarget{kind: mouseTargetNone}
		m.hoveredMouse = target
		return m, nil
	}

	if ev.Action != tea.MouseActionPress {
		return m, nil
	}

	m.pressedMouse = target
	m.dragOrigin = target
	m.hoveredMouse = target

	if ev.Button != tea.MouseButtonLeft {
		return m, nil
	}

	switch target.kind {
	case mouseTargetNone:
	case mouseTargetSettings:
		m = m.openSettingsView()
	case mouseTargetSearchInput:
		m = m.focusSearchInput()
	case mouseTargetSearchResult:
		if target.index >= 0 && target.index < len(m.searchRes) {
			doubleClick := m.lastClickMouse == target && time.Since(m.lastClickAt) <= 450*time.Millisecond
			m = m.selectSearchResult(target.index)
			m.lastClickMouse = target
			m.lastClickAt = time.Now()
			if doubleClick {
				return m, openCmd(m.searchRes[target.index].Path, true)
			}
		}
	case mouseTargetConfigRow:
		return m.activateConfigRow(target.index)
	case mouseTargetConfigExclude:
		m.cfgCursor = target.index + 3
	case mouseTargetPathOption:
		return m.handlePathOptionClick(target.index)
	case mouseTargetThemeOption:
		return m.handleThemeOptionClick(target.index)
	case mouseTargetModalInput:
		m.cfgInputActive = true
		return m, m.cfgInput.Focus()
	case mouseTargetModalOutside:
		if m.modal != deleteConfirmModal {
			m = m.closeModal()
		}
	}
	return m, nil
}

// noinspection GoAssignmentToReceiver
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.resizeComponents()
		return m, nil

	case countDoneMsg:
		if msg.err == nil {
			m.totalIndexed = msg.total
		} else {
			m.err = msg.err
		}
		if !m.busy && !m.startupScanAttempted {
			roots, err := defaultScanRoots(m.cfg)
			if err != nil {
				m.err = err
				m.status = "initial scan failed"
				m.startupScanAttempted = true
				m = m.openSettingsView()
				return m, nil
			}
			m.startupScanAttempted = true
			m.status = "initial scan in progress…"
			return m.startScanCmd(roots, "initial-scan", false)
		}
		return m, nil

	case searchDoneMsg:
		if msg.query == m.searchInput.Value() {
			m.searchRes = msg.results
			if m.searchCur >= len(m.searchRes) {
				m.searchCur = max(0, len(m.searchRes)-1)
			}
			m = m.syncSearchTableRows()
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
			m = m.openSettingsView()
			return m, countCmd(m.ctx, m.cfg.DBPath)
		}
		m.err = msg.err
		if msg.err == nil {
			m.status = fmt.Sprintf("re-index done: scanned=%d indexed=%d", msg.metrics.Scanned, msg.metrics.Indexed)
			m = m.focusSearchView()
		} else {
			m.status = "re-index failed"
			m = m.openSettingsView()
		}
		return m, countCmd(m.ctx, m.cfg.DBPath)

	case scanDoneMsg:
		m.scanCancel = nil
		m.busy = false
		m.lastMetrics = msg.metrics
		startup := msg.label == "initial-scan"
		manual := msg.label == "manual-scan"
		if errors.Is(msg.err, context.Canceled) {
			m.err = nil
			m.status = "scan canceled"
			if startup || manual {
				m = m.openSettingsView()
			}
			return m, countCmd(m.ctx, m.cfg.DBPath)
		}
		m.err = msg.err
		if msg.err == nil {
			m.status = fmt.Sprintf("%s done: scanned=%d indexed=%d", msg.label, msg.metrics.Scanned, msg.metrics.Indexed)
			if startup || manual {
				m = m.focusSearchView()
			}
		} else {
			m.status = msg.label + " failed"
			if startup || manual {
				m = m.openSettingsView()
			}
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

	case scanProgressTickMsg:
		if msg.session != m.scanSession || !m.busy || m.scanProgressSource == nil {
			return m, nil
		}
		if progress, ok := m.scanProgressSource.snapshot(msg.session); ok {
			m.scanProgress = progress
		}
		return m, scanProgressTickCmd(msg.session)

	case deleteResultDoneMsg:
		m.err = msg.err
		if msg.err != nil {
			m.status = "delete failed"
			return m, nil
		}
		m = m.removeDeletedResult(msg.entry)
		m.totalIndexed = msg.total
		m.status = "result deleted"
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+q":
			return m, tea.Quit
		}
		if m.modal != noModal {
			return m.handleModalKey(msg)
		}
		if m.mode == viewStartup {
			switch msg.String() {
			case "ctrl+x":
			default:
				return m, nil
			}
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
				m.mode = viewStartup
				return m.startScanCmd(roots, "manual-scan", false)
			}
		case "ctrl+s":
			if m.mode == viewSearch {
				m = m.openSettingsView()
				return m, nil
			}
		case "esc":
			if m.mode == viewConfig {
				m = m.focusSearchView()
				return m, nil
			}
		}

		switch m.mode {
		case viewSearch:
			return m.updateSearch(msg)
		case viewConfig:
			return m.updateConfig(msg)
		case viewStartup:
			return m, nil
		}
	}

	return m, nil
}

// noinspection GoAssignmentToReceiver
func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab":
		return m, nil
	case "up":
		m = m.setSearchCursor(m.searchCur - 1)
		return m, nil
	case "down":
		m = m.setSearchCursor(m.searchCur + 1)
		return m, nil
	case "enter":
		if len(m.searchRes) > 0 {
			m = m.setSearchCursor(m.searchCur)
			return m, openCmd(m.searchRes[m.searchCur].Path, true)
		}
		return m, nil
	case "ctrl+d", "delete":
		if len(m.searchRes) > 0 {
			m = m.setSearchCursor(m.searchCur)
			m.deleteIndex = m.searchCur
			m.deleteTarget = m.searchRes[m.searchCur]
			m.hasDeleteTarget = true
			m.modal = deleteConfirmModal
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchSeq++
	q := m.searchInput.Value()
	return m, tea.Batch(cmd, debounceCmd(m.searchSeq, q))
}

// noinspection GoAssignmentToReceiver
func (m model) updateConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	totalRows := 3 + len(m.cfg.Excludes) // 0 scan,1 theme,2 delete mode,3.. excludes
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
		return m.activateConfigRow(m.cfgCursor)
	case "d":
		exIdx := m.cfgCursor - 3
		m = m.removeExcludeAt(exIdx)
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
	case deleteConfirmModal:
		return m.handleDeleteConfirmModalKey(msg)
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
	m.hasDeleteTarget = false
	m.deleteIndex = 0
	m.deleteTarget = db.Entry{}
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
				m.err = err
				return m, nil
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
			m.err = err
			return m, nil
		}
		return next.closeModal(), nil
	case "esc":
		return m.closeModal(), nil
	}
	return m, nil
}

// noinspection GoAssignmentToReceiver
func (m model) handleThemeModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "j", "down":
		m.cfgThemeCursor = min(len(m.themes)-1, m.cfgThemeCursor+1)
	case "k", "up":
		m.cfgThemeCursor = max(0, m.cfgThemeCursor-1)
	case "enter":
		m.cfg.Theme = m.themes[m.cfgThemeCursor]
		m.theme = themeByName(m.cfg.Theme)
		m = m.resizeComponents()
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

// noinspection GoAssignmentToReceiver
func (m model) handleDeleteConfirmModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		if !m.hasDeleteTarget {
			return m.closeModal(), nil
		}
		entry := m.deleteTarget
		index := m.deleteIndex
		m = m.closeModal()
		m.status = "deleting result..."
		return m, deleteResultCmd(m.ctx, m.cfg, entry, index)
	case "esc", "n":
		m.status = "delete canceled"
		return m.closeModal(), nil
	}
	return m, nil
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

func (m model) removeDeletedResult(entry db.Entry) model {
	if len(m.searchRes) == 0 {
		return m.syncSearchTableRows()
	}
	clean := filepath.Clean(entry.Path)
	filtered := m.searchRes[:0]
	for _, res := range m.searchRes {
		resPath := filepath.Clean(res.Path)
		remove := resPath == clean
		if entry.IsDir && strings.HasPrefix(resPath, clean+string(os.PathSeparator)) {
			remove = true
		}
		if !remove {
			filtered = append(filtered, res)
		}
	}
	m.searchRes = filtered
	if len(m.searchRes) == 0 {
		m.searchCur = 0
	} else if m.searchCur >= len(m.searchRes) {
		m.searchCur = len(m.searchRes) - 1
	}
	return m.syncSearchTableRows()
}

// noinspection GoAssignmentToReceiver
func (m model) View() string {
	m = m.resizeComponents()
	return m.renderFrame()
}

func (m model) renderFrame() string {
	screenW, screenH := m.screenSize()
	bodyW, bodyH := m.bodySize()

	if m.mode == viewStartup {
		body := lipgloss.NewStyle().
			Width(bodyW).
			Height(bodyH).
			Render(m.renderContent(bodyW, bodyH))

		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.Border).
			Padding(0, 1).
			Width(screenW - 2).
			Height(screenH - 2).
			Render(body)
	}

	top := m.renderTopBar()
	help := m.renderHelp()
	errLine := m.renderError()
	contentH := max(3, bodyH-lipgloss.Height(top)-lipgloss.Height(errLine)-lipgloss.Height(help))
	content := m.renderContent(bodyW, contentH)
	if m.modal != noModal {
		content = m.renderModal(bodyW, contentH)
	}
	content = padBlockHeight(content, contentH, bodyW)

	parts := make([]string, 0, 5)
	parts = append(parts, top)
	parts = append(parts, content)
	if errLine != "" {
		parts = append(parts, errLine)
	}
	prefixH := lipgloss.Height(lipgloss.JoinVertical(lipgloss.Left, parts...))
	spacerH := max(0, bodyH-prefixH-lipgloss.Height(help))
	if spacerH > 0 {
		parts = append(parts, blankBlock(spacerH, bodyW))
	}
	parts = append(parts, help)

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

func padBlockHeight(content string, height int, width int) string {
	for lipgloss.Height(content) < height {
		content += "\n" + strings.Repeat(" ", max(1, width))
	}
	return content
}

func blankBlock(height int, width int) string {
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", max(1, width))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderContent(width, height int) string {
	var content string
	switch m.mode {
	case viewStartup:
		content = m.viewStartup(width, height)
	case viewSearch:
		content = m.viewSearch(width, height)
	case viewConfig:
		content = m.viewConfig(width, height)
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, content)
}

func (m model) renderError() string {
	if m.err != nil {
		maxWidth := max(20, m.width-6)
		return m.theme.Err.Render(trimMiddle("error: "+m.err.Error(), maxWidth))
	}
	return ""
}

func (m model) renderHelp() string {
	keys := "j/k move • enter select • ctrl+g scan now • ctrl+x stop scan • esc back • ctrl+q quit"
	if m.modal != noModal {
		keys = "j/k or wheel move • click select • esc/outside click close modal • ctrl+q quit"
	} else if m.mode == viewStartup {
		keys = "ctrl+x cancel • ctrl+q quit"
	} else if m.mode == viewConfig {
		keys = "j/k or wheel move • click edit/toggle • right-click exclude removes • a add • d remove • esc search • ctrl+q quit"
	} else if m.mode == viewSearch {
		keys = "↑/↓ or mouse move • enter/double-click open • ctrl+d/delete/right-click delete • ctrl+q quit"
	}
	return m.theme.Muted.Render(trimMiddle("keys: "+keys, max(20, m.width-6)))
}

func (m model) renderTopBar() string {
	bodyW, _ := m.bodySize()
	innerWidth := max(24, min(bodyW, 120))

	scopeLen := max(8, innerWidth/4)
	basePlain := fmt.Sprintf("◌ GoEverything %d indexed scope %s", m.totalIndexed, trimMiddle(m.cfg.DefaultScanPath, scopeLen))
	lastPlain := "last scan " + prettyElapsed(m.lastMetrics.Elapsed)

	lastView := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Badge).
		Padding(0, 1).
		Render(m.theme.Muted.Render(lastPlain))
	statusView := ""
	if m.busy {
		statusText := m.theme.Highlight.Bold(true).Render("● scanning")
		statusView = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(m.theme.BorderHi).
			Padding(0, 1).
			Render(statusText)
	}

	mainView := m.theme.Header.Render(basePlain)
	contentParts := []string{mainView, "  ", lastView}
	if statusView != "" {
		contentParts = append(contentParts, "  ", statusView)
	}
	content := lipgloss.JoinHorizontal(lipgloss.Center, contentParts...)
	if lipgloss.Width(content) > innerWidth-2 {
		shortBase := trimMiddle(basePlain, max(12, innerWidth/3))
		mainView = m.theme.Header.Render(shortBase)
		if statusView != "" {
			content = lipgloss.JoinHorizontal(lipgloss.Center, mainView, "  ", statusView)
		} else {
			content = lipgloss.JoinHorizontal(lipgloss.Center, mainView, "  ", lastView)
		}
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

func (m model) itemStyleState(active bool, hovered bool) lipgloss.Style {
	st := m.panelStyle()
	if active {
		st = st.BorderForeground(m.theme.BorderHi).Background(m.theme.SurfaceBG)
	} else if hovered {
		st = st.BorderForeground(m.theme.Badge).Background(m.theme.SurfaceBG)
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

func (m model) viewStartup(width, height int) string {
	message := "Scanning index…"
	switch m.activeScanLabel {
	case "initial-scan":
		message = "Scanning before search opens…"
	case "reindex":
		message = "Re-indexing…"
	}
	if !m.startupScanAttempted && !m.busy {
		message = "Preparing initial scan…"
	}
	lines := []string{
		m.theme.Highlight.Render(message),
	}
	p := m.scanProgress
	current := "waiting for first path…"
	if strings.TrimSpace(p.CurrentPath) != "" {
		current = trimMiddle(p.CurrentPath, max(18, min(86, width-16)))
	}
	lines = append(lines,
		"",
		m.theme.Title.Render("PROGRESS"),
		fmt.Sprintf("scanned: %d", p.Scanned),
		fmt.Sprintf("indexed: %d", p.Indexed),
		fmt.Sprintf("skipped: %d", p.Skipped),
		fmt.Sprintf("elapsed: %s", prettyDuration(p.Elapsed)),
		fmt.Sprintf("speed: %.1f files/s", p.FilesPerSecond),
		m.theme.Muted.Render("current: "+current),
	)
	cardHeight := 13

	card := m.panelStyle().
		Width(max(32, min(78, width-4))).
		Height(max(3, min(cardHeight, height-2))).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, card)
}

func (m model) settingsButtonView() string {
	return m.badgeStyle().Render("⚙ Settings [Ctrl+S]")
}

func (m model) viewSearch(width, height int) string {
	var lines []string
	topRow, _ := m.searchTopRow(width)
	lines = append(lines, topRow)

	lines = append(lines, m.searchBoxView())

	if len(m.searchRes) == 0 {
		lines = append(lines, m.renderEmptySearchResults())
	} else {
		lines = append(lines, m.theme.Title.Render("RESULTS"))
		lines = append(lines, m.renderSearchResults())
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

func (m model) renderEmptySearchResults() string {
	tableW := max(searchColumnsWidth(m.searchTable.Columns()), m.searchTable.Width())
	blockH := max(3, m.searchTable.Height()+1)
	message := lipgloss.PlaceHorizontal(tableW, lipgloss.Center, m.theme.Muted.Render("No results"))
	topPad := max(0, (blockH-1)/2)
	bottomPad := max(0, blockH-topPad-1)

	lines := make([]string, 0, blockH)
	for range topPad {
		lines = append(lines, "")
	}
	lines = append(lines, message)
	for range bottomPad {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m model) renderSearchResults() string {
	cols := m.searchTable.Columns()
	tableW := max(searchColumnsWidth(cols), m.searchTable.Width())
	header := m.renderSearchCells(cols, []string{"Type", "Name", "Directory", "Size"})
	lines := []string{m.theme.Title.Render(padToWidth(header, tableW))}

	if len(m.searchRes) == 0 {
		return strings.Join(lines, "\n")
	}

	cursor, start, end := m.searchVisibleWindow()

	for i := start; i < end; i++ {
		row := padToWidth(m.renderSearchCells(cols, searchEntryValues(m.searchRes[i])), tableW)
		if i == cursor {
			row = lipgloss.NewStyle().
				Background(m.theme.SelectBG).
				Foreground(m.theme.SelectFG).
				Bold(true).
				Render(row)
		} else if m.mouseHoverMatches(mouseTargetSearchResult, i) {
			row = lipgloss.NewStyle().
				Background(m.theme.SurfaceBG).
				Foreground(m.theme.SelectFG).
				Render(row)
		} else {
			row = m.theme.Text.Render(row)
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}

func (m model) renderSearchCells(cols []table.Column, values []string) string {
	cells := make([]string, 0, min(len(cols), len(values)))
	for i, col := range cols {
		if i >= len(values) || col.Width <= 0 {
			continue
		}
		cells = append(cells, padToWidth(trimMiddle(values[i], col.Width), col.Width))
	}
	return strings.Join(cells, "")
}

func searchEntryValues(entry db.Entry) []string {
	kind := "file"
	if entry.IsDir {
		kind = "dir"
	}
	sizeText := "-"
	if !entry.IsDir {
		sizeText = fmt.Sprintf("%.1f KB", float64(entry.Size)/1024)
	}
	return []string{kind, entry.Name, filepath.Dir(entry.Path), sizeText}
}

func searchColumnsWidth(cols []table.Column) int {
	width := 0
	for _, col := range cols {
		if col.Width > 0 {
			width += col.Width
		}
	}
	return width
}

func padToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	current := lipgloss.Width(s)
	if current >= width {
		return s
	}
	return s + strings.Repeat(" ", width-current)
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
	case deleteConfirmModal:
		modal = m.renderDeleteConfirmModal(width)
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
		Width(m.modalWidth(width))
}

func (m model) modalWidth(width int) int {
	return max(28, min(72, width-8))
}

func (m model) modalContentWidth(width int) int {
	return max(20, m.modalWidth(width)-4)
}

func (m model) renderModalInput(width int) string {
	contentW := max(18, m.modalContentWidth(width)-2)
	inputW := max(12, contentW-4)
	input := m.cfgInput
	input.Width = inputW
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(m.theme.Input).
		Padding(0, 1).
		Width(contentW).
		Render(input.View())
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
			m.renderModalInput(width),
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
		} else if m.mouseHoverMatches(mouseTargetPathOption, i) {
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SurfaceBG)
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
		} else if m.mouseHoverMatches(mouseTargetThemeOption, i) {
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SurfaceBG)
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
		m.renderModalInput(width),
	}
	return m.modalStyle(width).Render(strings.Join(lines, "\n"))
}

func (m model) renderDeleteConfirmModal(width int) string {
	path := "(no selection)"
	kind := "result"
	if m.hasDeleteTarget {
		path = m.deleteTarget.Path
		if m.deleteTarget.IsDir {
			kind = "folder"
		} else {
			kind = "file"
		}
	}
	lines := []string{
		m.theme.Title.Render("DELETE RESULT"),
		m.theme.Warn.Render("This will delete the selected " + kind + "."),
		"",
		"mode: " + deleteModeLabel(m.cfg.DeleteMode),
		"path: " + trimMiddle(path, max(18, min(66, width-18))),
		"",
		m.theme.Muted.Render("enter/y confirm • esc/n cancel"),
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

func prettyDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		return "<1s"
	}
	return d.Round(time.Second).String()
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
		return m.itemStyleState(m.cfgCursor == idx, m.mouseHoverMatches(mouseTargetConfigRow, idx)).Width(width).Render(body)
	}

	leftRows := []string{
		m.theme.Title.Render("SETTINGS"),
		configRow(0, "scan location", m.cfg.DefaultScanPath, "↵ change", leftInnerW),
		configRow(1, "theme", m.cfg.Theme, "↵ select", leftInnerW),
		configRow(2, "delete mode", deleteModeLabel(m.cfg.DeleteMode), "↵ toggle", leftInnerW),
	}
	leftPanel := strings.Join(leftRows, "\n\n")

	rightRows := []string{m.theme.Title.Render("EXCLUDE PATTERNS")}
	var exLines []string
	for i, ex := range m.cfg.Excludes {
		idx := i + 3
		row := "◌ " + trimMiddle(ex, max(12, rightInnerW-6))
		st := m.theme.Text
		if m.cfgCursor == idx {
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SelectBG).Bold(true)
		} else if m.mouseHoverMatches(mouseTargetConfigExclude, i) {
			st = lipgloss.NewStyle().Foreground(m.theme.SelectFG).Background(m.theme.SurfaceBG)
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

func nextDeleteMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), config.DeleteModePermanent) {
		return config.DeleteModeTrash
	}
	return config.DeleteModePermanent
}

func deleteModeLabel(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), config.DeleteModePermanent) {
		return "Permanent"
	}
	return "Trash"
}

func searchCmd(ctx context.Context, dbPath, query string) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, dbPath)
		if err != nil {
			return searchDoneMsg{query: query, err: err}
		}
		defer func() { _ = store.Close() }()
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
		defer func() { _ = store.Close() }()
		total, err := store.Count(ctx)
		return countDoneMsg{total: total, err: err}
	}
}

func deleteResultCmd(ctx context.Context, cfg config.Config, entry db.Entry, index int) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(entry.Path) == "" {
			return deleteResultDoneMsg{index: index, entry: entry, err: errors.New("path is required")}
		}
		isDir := entry.IsDir
		info, statErr := os.Stat(entry.Path)
		switch {
		case statErr == nil:
			isDir = info.IsDir()
			if strings.EqualFold(cfg.DeleteMode, config.DeleteModePermanent) {
				if err := os.RemoveAll(entry.Path); err != nil {
					return deleteResultDoneMsg{index: index, entry: entry, err: err}
				}
			} else if err := moveToTrash(entry.Path); err != nil {
				return deleteResultDoneMsg{index: index, entry: entry, err: err}
			}
		case errors.Is(statErr, os.ErrNotExist):
			// The file already disappeared; keep the index consistent below.
		default:
			return deleteResultDoneMsg{index: index, entry: entry, err: statErr}
		}
		entry.IsDir = isDir

		store, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return deleteResultDoneMsg{index: index, entry: entry, err: err}
		}
		defer func() { _ = store.Close() }()

		if isDir {
			err = store.DeleteByPrefix(ctx, entry.Path)
		} else {
			err = store.DeleteByPath(ctx, entry.Path)
		}
		if err != nil {
			return deleteResultDoneMsg{index: index, entry: entry, err: err}
		}
		total, err := store.Count(ctx)
		return deleteResultDoneMsg{index: index, entry: entry, total: total, err: err}
	}
}

func moveToTrash(path string) error {
	script := `tell application "Finder" to delete POSIX file ` + appleScriptQuote(path)
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptQuote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func debounceCmd(seq int, query string) tea.Cmd {
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg {
		return debounceSearchMsg{seq: seq, query: query}
	})
}

func scanProgressTickCmd(session int) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return scanProgressTickMsg{session: session}
	})
}

func reindexCmd(ctx context.Context, cfg config.Config, roots []string, progress func(scanner.Progress)) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return reindexDoneMsg{err: err}
		}
		defer func() { _ = store.Close() }()

		if err := store.ReindexFTS(ctx); err != nil {
			return reindexDoneMsg{err: err}
		}
		r := scanner.Runner{
			Indexer:  store,
			Workers:  scanner.DefaultWorkerCount(),
			Batch:    2000,
			Exclude:  cfg.Excludes,
			Progress: progress,
		}
		metrics, err := r.Scan(ctx, roots)
		return reindexDoneMsg{metrics: metrics, err: watcher.WithPermissionHint(err)}
	}
}

func scanRootsCmd(ctx context.Context, cfg config.Config, roots []string, label string, progress func(scanner.Progress)) tea.Cmd {
	return func() tea.Msg {
		store, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return scanDoneMsg{err: err, label: label}
		}
		defer func() { _ = store.Close() }()

		r := scanner.Runner{
			Indexer:  store,
			Workers:  scanner.DefaultWorkerCount(),
			Batch:    2000,
			Exclude:  cfg.Excludes,
			Progress: progress,
		}
		metrics, err := r.Scan(ctx, roots)
		return scanDoneMsg{metrics: metrics, err: watcher.WithPermissionHint(err), label: label}
	}
}

func (m model) startScanCmd(roots []string, label string, reindex bool) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.scanCancel = cancel
	m.busy = true
	m.activeScanLabel = label
	m.scanSession++
	session := m.scanSession
	m.scanProgress = scanner.Progress{}
	if m.scanProgressSource == nil {
		m.scanProgressSource = newScanProgressSource()
	}
	m.scanProgressSource.reset(session)
	progress := func(p scanner.Progress) {
		m.scanProgressSource.update(session, p)
	}
	if reindex {
		return m, tea.Batch(reindexCmd(ctx, m.cfg, roots, progress), scanProgressTickCmd(session))
	}
	return m, tea.Batch(scanRootsCmd(ctx, m.cfg, roots, label, progress), scanProgressTickCmd(session))
}

func openCmd(path string, reveal bool) tea.Cmd {
	return func() tea.Msg {
		cmd := openCommand(path, reveal)
		_ = cmd.Start()
		return nil
	}
}

func openCommand(path string, reveal bool) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		if reveal {
			return exec.Command("explorer.exe", "/select,"+path)
		}
		return exec.Command("explorer.exe", path)
	case "darwin":
		if reveal {
			return exec.Command("open", "-R", path)
		}
		return exec.Command("open", path)
	default:
		if reveal {
			return exec.Command("xdg-open", filepath.Dir(path))
		}
		return exec.Command("xdg-open", path)
	}
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
			Border:    "#504945",
			BorderHi:  "#83a598",
			SurfaceBG: "#282828",
			Badge:     "#665c54",
			Input:     "#83a598",
			InputBG:   "#3c3836",
			InputFG:   "#ebdbb2",
			SelectBG:  "#3c3836",
			SelectFG:  "#fbf1c7",
			BusyFG:    "#b8bb26",
			BusyBG:    "#3c3836",
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
			Border:    "#45475a",
			BorderHi:  "#89dceb",
			SurfaceBG: "#1e1e2e",
			Badge:     "#585b70",
			Input:     "#89dceb",
			InputBG:   "#313244",
			InputFG:   "#f5e0dc",
			SelectBG:  "#313244",
			SelectFG:  "#f5e0dc",
			BusyFG:    "#a6e3a1",
			BusyBG:    "#313244",
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
			Border:    "#2d3655",
			BorderHi:  "#5a86ff",
			SurfaceBG: "#1b2138",
			Badge:     "#3a4668",
			Input:     "#356cff",
			InputBG:   "#2f3b63",
			InputFG:   "#ffffff",
			SelectBG:  "#2f3b63",
			SelectFG:  "#ffffff",
			BusyFG:    "#9ece6a",
			BusyBG:    "#1f2335",
		}
	}
}
