// Package ui implements the dashboard: session list on the left, live
// conversation on the right. Typing goes straight into the selected session.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonasN5/claude-minimal/internal/config"
	"github.com/jonasN5/claude-minimal/internal/session"
)

const leftWidth = 34

type mode int

const (
	modeMain mode = iota
	modeWizard
	modeConfirmDelete
)

type repaintMsg struct{}

// deleteDoneMsg reports completion of an async session deletion.
type deleteDoneMsg struct {
	name string
	err  error
}

// App is the root bubbletea model.
type App struct {
	cfg   *config.Config
	store *session.Store

	sessions []*session.Session
	sel      int

	width, height int
	focusList     bool
	mode          mode
	wizard        *wizard
	confirmMsg    string
	errMsg        string
	flash         string

	// In-pane mouse selection (cell coordinates within the conversation pane).
	selecting bool
	selAnchor [2]int
	selPos    [2]int

	// Sessions currently being deleted (teardown hooks run in the background).
	deleting map[string]bool

	program *tea.Program
	notify  chan struct{}
}

// New creates the app model and loads sessions from disk.
func New(cfg *config.Config) (*App, error) {
	store := session.NewStore(cfg)
	sessions, err := store.List()
	if err != nil {
		return nil, err
	}
	return &App{
		cfg:      cfg,
		store:    store,
		sessions: sessions,
		notify:   make(chan struct{}, 1),
		deleting: map[string]bool{},
	}, nil
}

// SetProgram wires the tea.Program and starts the repaint coalescer.
func (a *App) SetProgram(p *tea.Program) {
	a.program = p
	go func() {
		for range a.notify {
			p.Send(repaintMsg{})
			time.Sleep(33 * time.Millisecond) // coalesce bursts to ~30fps
		}
	}()
}

func (a *App) requestRepaint() {
	select {
	case a.notify <- struct{}{}:
	default:
	}
}

// Shutdown kills all running sessions, saving their context tails.
func (a *App) Shutdown() {
	for _, s := range a.sessions {
		if s.Running() {
			s.Proc.Kill()
		}
	}
}

func (a *App) Init() tea.Cmd { return nil }

func (a *App) termSize() (cols, rows int) {
	// The conversation pane is drawn inside a rounded border (2 cols/rows)
	// with a one-line help bar below.
	cols = a.width - leftWidth - 2
	rows = a.height - 3
	if cols < 10 {
		cols = 10
	}
	if rows < 4 {
		rows = 4
	}
	return
}

func (a *App) selected() *session.Session {
	if a.sel >= 0 && a.sel < len(a.sessions) {
		return a.sessions[a.sel]
	}
	return nil
}

func (a *App) spawn(s *session.Session) {
	cols, rows := a.termSize()
	// User-approved: pre-trust only workspaces this tool created itself, so
	// Claude doesn't show the folder-trust dialog for every new session.
	if err := session.EnsureTrusted(s.Workspace()); err != nil {
		a.errMsg = "pre-trust failed: " + err.Error()
	}
	argv := s.LaunchArgv(a.cfg)
	proc, err := session.Start(s.Workspace(), argv, cols, rows, s.ContextFile(), a.cfg.TailLines, a.requestRepaint)
	if err != nil {
		a.errMsg = "launch failed: " + err.Error()
		return
	}
	s.Proc = proc
	s.MarkStarted()
	a.errMsg = ""
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case repaintMsg:
		return a, nil

	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.selecting = false
		cols, rows := a.termSize()
		for _, s := range a.sessions {
			if s.Running() {
				s.Proc.Resize(cols, rows)
			}
		}
		return a, nil

	case deleteDoneMsg:
		delete(a.deleting, msg.name)
		if msg.err != nil {
			a.errMsg = "delete " + msg.name + ": " + msg.err.Error()
		}
		a.flash = ""
		a.afterDelete()
		return a, nil

	case tea.MouseMsg:
		if a.mode == modeMain {
			return a.updateMouse(msg)
		}
		return a, nil

	case tea.KeyMsg:
		switch a.mode {
		case modeWizard:
			return a.updateWizard(msg)
		case modeConfirmDelete:
			return a.updateConfirmDelete(msg)
		}
		return a.updateMain(msg)
	}
	return a, nil
}

func (a *App) updateMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a.flash = ""
	// Global shortcuts, never forwarded to the session.
	switch msg.String() {
	case "ctrl+q", "alt+q":
		a.Shutdown()
		return a, tea.Quit
	case "alt+up", "ctrl+up":
		a.moveSelection(-1)
		return a, nil
	case "alt+down", "ctrl+down":
		a.moveSelection(1)
		return a, nil
	case "alt+n":
		a.wizard = newWizard(a.cfg)
		a.mode = modeWizard
		return a, a.wizard.name.Focus()
	case "alt+d":
		if s := a.selected(); s != nil && !a.deleting[s.Name] {
			a.confirmMsg = ""
			a.mode = modeConfirmDelete
		}
		return a, nil
	case "alt+l":
		a.focusList = !a.focusList
		return a, nil
	}

	if a.focusList {
		switch msg.String() {
		case "up", "k":
			a.moveSelection(-1)
		case "down", "j":
			a.moveSelection(1)
		case "n":
			a.wizard = newWizard(a.cfg)
			a.mode = modeWizard
			return a, a.wizard.name.Focus()
		case "d":
			if s := a.selected(); s != nil && !a.deleting[s.Name] {
				a.confirmMsg = ""
				a.mode = modeConfirmDelete
			}
		case "q":
			a.Shutdown()
			return a, tea.Quit
		case "enter", "esc":
			a.focusList = false
		}
		return a, nil
	}

	// Everything else goes to the selected session.
	s := a.selected()
	if s == nil {
		if msg.String() == "enter" || msg.String() == "n" {
			a.wizard = newWizard(a.cfg)
			a.mode = modeWizard
			return a, a.wizard.name.Focus()
		}
		return a, nil
	}
	if a.deleting[s.Name] {
		return a, nil
	}
	if !s.Running() {
		// First keypress revives the session; the key itself is not forwarded.
		a.spawn(s)
		return a, nil
	}
	if b := keyToBytes(msg); b != nil {
		s.Proc.Write(b)
	}
	return a, nil
}

// paneCell maps screen coordinates to conversation-pane cell coordinates.
func (a *App) paneCell(mx, my int) (x, y int, inside bool) {
	cols, rows := a.termSize()
	x, y = mx-leftWidth-1, my-1
	inside = x >= 0 && x < cols && y >= 0 && y < rows
	if x < 0 {
		x = 0
	}
	if x >= cols {
		x = cols - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= rows {
		y = rows - 1
	}
	return
}

// listItemAt maps a screen row in the left pane to a session index.
// viewList layout: 4 header lines, then 3 lines per item (2 text + 1 blank).
func (a *App) listItemAt(my int) (int, bool) {
	if my < 4 || (my-4)%3 == 2 {
		return 0, false
	}
	i := (my - 4) / 3
	return i, i < len(a.sessions)
}

func (a *App) selRange() *SelRange {
	x1, y1 := a.selAnchor[0], a.selAnchor[1]
	x2, y2 := a.selPos[0], a.selPos[1]
	if y1 > y2 || (y1 == y2 && x1 > x2) {
		x1, y1, x2, y2 = x2, y2, x1, y1
	}
	return &SelRange{X1: x1, Y1: y1, X2: x2, Y2: y2}
}

func (a *App) selectionText() string {
	s := a.selected()
	if s == nil || s.Proc == nil {
		return ""
	}
	cols, _ := a.termSize()
	r := a.selRange()
	vt := s.Proc.VT
	vt.Lock()
	defer vt.Unlock()
	var lines []string
	for y := r.Y1; y <= r.Y2; y++ {
		sx, ex := 0, cols-1
		if y == r.Y1 {
			sx = r.X1
		}
		if y == r.Y2 {
			ex = r.X2
		}
		var b strings.Builder
		for x := sx; x <= ex; x++ {
			ch := vt.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func (a *App) updateMouse(m tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch m.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		// Match Terminal.app's alt-screen behavior: wheel = arrow keys.
		if s := a.selected(); s != nil && s.Running() {
			seq := "\x1b[A"
			if m.Button == tea.MouseButtonWheelDown {
				seq = "\x1b[B"
			}
			s.Proc.Write([]byte(strings.Repeat(seq, 3)))
		}
		return a, nil
	}
	switch m.Action {
	case tea.MouseActionPress:
		if m.Button != tea.MouseButtonLeft {
			return a, nil
		}
		a.flash = ""
		if x, y, inside := a.paneCell(m.X, m.Y); inside {
			a.selecting = true
			a.selAnchor = [2]int{x, y}
			a.selPos = a.selAnchor
			a.focusList = false
			return a, nil
		}
		if m.X < leftWidth {
			if i, ok := a.listItemAt(m.Y); ok {
				a.sel = i
				a.focusList = false
			}
		}
	case tea.MouseActionMotion:
		if a.selecting {
			x, y, _ := a.paneCell(m.X, m.Y)
			a.selPos = [2]int{x, y}
		}
	case tea.MouseActionRelease:
		if !a.selecting {
			return a, nil
		}
		a.selecting = false
		if a.selAnchor == a.selPos {
			return a, nil // plain click, nothing to copy
		}
		if text := a.selectionText(); strings.TrimSpace(text) != "" {
			if err := clipboard.WriteAll(text); err != nil {
				a.flash = "copy failed: " + err.Error()
			} else {
				a.flash = fmt.Sprintf("✓ copied %d chars", len(text))
			}
		}
	}
	return a, nil
}

func (a *App) moveSelection(delta int) {
	if len(a.sessions) == 0 {
		return
	}
	a.sel += delta
	if a.sel < 0 {
		a.sel = 0
	}
	if a.sel >= len(a.sessions) {
		a.sel = len(a.sessions) - 1
	}
}

func (a *App) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := a.selected()
	if s == nil {
		a.mode = modeMain
		return a, nil
	}
	switch msg.String() {
	case "y":
		// The safety check is fast and runs here; the slow teardown
		// (docker, DB, hooks) runs in the background.
		if dirty := s.DirtyProjects(); len(dirty) > 0 {
			a.confirmMsg = "uncommitted or unpushed work in: " + strings.Join(dirty, ", ") +
				" — press f to force-delete anyway"
			return a, nil
		}
		return a, a.startDelete(s)
	case "f":
		return a, a.startDelete(s)
	case "n", "esc", "q":
		a.mode = modeMain
	}
	return a, nil
}

func (a *App) startDelete(s *session.Session) tea.Cmd {
	a.deleting[s.Name] = true
	a.mode = modeMain
	a.flash = "deleting " + s.Name + "…"
	return func() tea.Msg {
		return deleteDoneMsg{s.Name, a.store.Delete(s, true)}
	}
}

// reload re-reads sessions from disk, carrying live processes over from the
// previous list so running sessions are not orphaned.
func (a *App) reload() {
	old := make(map[string]*session.Session, len(a.sessions))
	for _, s := range a.sessions {
		old[s.Name] = s
	}
	fresh, _ := a.store.List()
	for _, s := range fresh {
		if o, ok := old[s.Name]; ok {
			s.Proc = o.Proc
		}
	}
	a.sessions = fresh
}

func (a *App) afterDelete() {
	a.reload()
	if a.sel >= len(a.sessions) {
		a.sel = len(a.sessions) - 1
	}
	if a.sel < 0 {
		a.sel = 0
	}
}

func (a *App) updateWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	done, cancelled, cmd := a.wizard.update(msg)
	if cancelled {
		a.mode = modeMain
		return a, nil
	}
	if done {
		s, err := a.store.Create(a.wizard.name.Value(), a.wizard.chosen())
		if err != nil {
			a.wizard.errMsg = err.Error()
			return a, nil
		}
		a.reload()
		for i, ss := range a.sessions {
			if ss.Name == s.Name {
				a.sel = i
			}
		}
		a.mode = modeMain
		a.focusList = false
		a.spawn(a.sessions[a.sel])
	}
	return a, cmd
}

// --- View ---

var (
	accent     = lipgloss.Color("105") // periwinkle, à la claude-squad
	selBg      = lipgloss.Color("189")
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	chipStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(accent).Padding(0, 1)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	keyStyle   = lipgloss.NewStyle().Foreground(accent).Bold(true)

	nameStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	selNameStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("235")).Background(selBg)
	selDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Background(selBg)
	runDotStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	offDotStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	paneBorder    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent)
	paneBorderDim = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))

	// selStyle is kept for the wizard's cursor line.
	selStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("235")).Background(selBg)
)

func (a *App) View() string {
	if a.width == 0 {
		return "loading..."
	}
	switch a.mode {
	case modeWizard:
		return a.wizard.view(a.width, a.height)
	case modeConfirmDelete:
		return a.viewConfirmDelete()
	}
	if len(a.sessions) == 0 {
		return a.viewWelcome()
	}
	left := a.viewList()
	right := a.viewTerminal()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return body + "\n" + a.viewStatusBar()
}

func (a *App) prettyRoot() string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(a.cfg.Root, home) {
		return "~" + strings.TrimPrefix(a.cfg.Root, home)
	}
	return a.cfg.Root
}

func (a *App) viewWelcome() string {
	content := lipgloss.JoinVertical(lipgloss.Center,
		chipStyle.Render("claude-minimal"),
		"",
		dimStyle.Render("Fast session manager for Claude Code"),
		dimStyle.Render("root: "+a.prettyRoot()),
		"",
		keyStyle.Render("⌥n")+"  create your first session",
	)
	screen := lipgloss.Place(a.width, a.height-1, lipgloss.Center, lipgloss.Center, content)
	return screen + "\n" + a.viewStatusBar()
}

func truncate(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	r := []rune(s)
	if max < 1 {
		return ""
	}
	for len(r) > 0 && lipgloss.Width(string(r))+1 > max {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

func (a *App) renderItem(i int) []string {
	s := a.sessions[i]
	selected := i == a.sel

	dot, dotStyle := "○", offDotStyle
	if s.Running() {
		dot, dotStyle = "●", runDotStyle
	}
	if a.deleting[s.Name] {
		dot, dotStyle = "…", offDotStyle
	}
	num := fmt.Sprintf(" %d. ", i+1)
	name := truncate(s.Name, leftWidth-len(num)-4)
	line1 := num + name
	pad1 := leftWidth - lipgloss.Width(line1) - 3
	if pad1 < 0 {
		pad1 = 0
	}

	proj := "free-form"
	if len(s.Projects) > 0 {
		names := make([]string, len(s.Projects))
		for j, p := range s.Projects {
			names[j] = p.Name
		}
		proj = strings.Join(names, ", ")
	}
	line2 := strings.Repeat(" ", len(num)) + truncate(proj, leftWidth-len(num)-2)
	pad2 := leftWidth - lipgloss.Width(line2)
	if pad2 < 0 {
		pad2 = 0
	}

	if selected {
		return []string{
			selNameStyle.Render(line1+strings.Repeat(" ", pad1)) +
				dotStyle.Background(selBg).Render(dot) + selDimStyle.Render("  "),
			selDimStyle.Render(line2 + strings.Repeat(" ", pad2)),
		}
	}
	return []string{
		nameStyle.Render(line1) + strings.Repeat(" ", pad1) + dotStyle.Render(dot) + "  ",
		dimStyle.Render(line2),
	}
}

func (a *App) viewList() string {
	lines := []string{
		"",
		" " + chipStyle.Render("Sessions"),
		" " + dimStyle.Render(truncate(a.prettyRoot(), leftWidth-2)),
		"",
	}
	for i := range a.sessions {
		lines = append(lines, a.renderItem(i)...)
		lines = append(lines, "")
	}
	height := a.height - 1
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lipgloss.NewStyle().Width(leftWidth).MaxWidth(leftWidth).Render(strings.Join(lines, "\n"))
}

func (a *App) viewTerminal() string {
	cols, rows := a.termSize()
	s := a.selected()
	var inner string
	switch {
	case s == nil:
		inner = centered(cols, rows, dimStyle.Render("No session selected."))
	case a.deleting[s.Name]:
		inner = centered(cols, rows, dimStyle.Render("Deleting "+s.Name+"…\n\nrunning teardown hooks (see teardown.log)"))
	case s.Proc == nil:
		inner = centered(cols, rows,
			dimStyle.Render(fmt.Sprintf("Session %q is not running.", s.Name))+"\n\n"+
				dimStyle.Render("Press ")+keyStyle.Render("enter")+
				dimStyle.Render(" to resume with previous context\n(claude --continue + context.md)"))
	default:
		var sel *SelRange
		if a.selecting {
			sel = a.selRange()
		}
		inner = RenderTerminal(s.Proc.VT, cols, rows, !a.focusList, sel)
		if s.Proc.Exited() {
			lines := strings.Split(inner, "\n")
			note := errStyle.Render(" [exited — press enter to restart] ")
			if len(lines) > 0 {
				lines[len(lines)-1] = note
			}
			inner = strings.Join(lines, "\n")
		}
	}
	border := paneBorder
	if a.focusList {
		border = paneBorderDim
	}
	return border.Render(inner)
}

func centered(cols, rows int, text string) string {
	return lipgloss.Place(cols, rows, lipgloss.Center, lipgloss.Center, text)
}

func (a *App) viewStatusBar() string {
	sep := dimStyle.Render(" · ")
	bar := strings.Join([]string{
		keyStyle.Render("⌥n") + dimStyle.Render(" new"),
		keyStyle.Render("⌥d") + dimStyle.Render(" kill"),
		keyStyle.Render("⌥↑/↓") + dimStyle.Render(" switch"),
		keyStyle.Render("⌥l") + dimStyle.Render(" list"),
		keyStyle.Render("⌥q") + dimStyle.Render(" quit"),
	}, sep)
	if a.focusList {
		bar += dimStyle.Render("   [list: j/k, enter]")
	}
	if a.flash != "" {
		bar += "  " + keyStyle.Render(a.flash)
	}
	if a.errMsg != "" {
		bar += "  " + errStyle.Render(a.errMsg)
	}
	return lipgloss.Place(a.width, 1, lipgloss.Center, lipgloss.Center, bar)
}

func (a *App) viewConfirmDelete() string {
	s := a.selected()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Delete session "+s.Name+"?") + "\n\n")
	for _, p := range s.Projects {
		b.WriteString("  will remove worktree " + p.WorktreePath + "\n")
		if p.Teardown != "" {
			b.WriteString(dimStyle.Render("  will run teardown hook for "+p.Name) + "\n")
		}
	}
	b.WriteString("\n" + keyStyle.Render("y") + " delete   " +
		keyStyle.Render("f") + " force   " + keyStyle.Render("esc") + " cancel\n")
	if a.confirmMsg != "" {
		b.WriteString("\n" + errStyle.Render(a.confirmMsg) + "\n")
	}
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, b.String())
}
