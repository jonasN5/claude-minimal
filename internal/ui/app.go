// Package ui implements the dashboard: session list on the left, live
// conversation on the right. Typing goes straight into the selected session.
package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonasN5/claude-minimal/internal/config"
	"github.com/jonasN5/claude-minimal/internal/session"
)

const leftWidth = 30

type mode int

const (
	modeMain mode = iota
	modeWizard
	modeConfirmDelete
)

type repaintMsg struct{}

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
	cols = a.width - leftWidth - 1
	rows = a.height - 1
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
		cols, rows := a.termSize()
		for _, s := range a.sessions {
			if s.Running() {
				s.Proc.Resize(cols, rows)
			}
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
		if s := a.selected(); s != nil {
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
			if a.selected() != nil {
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
		if err := a.store.Delete(s, false); err != nil {
			a.confirmMsg = err.Error() + " — press f to force-delete anyway"
			return a, nil
		}
		a.afterDelete()
	case "f":
		if err := a.store.Delete(s, true); err != nil {
			a.confirmMsg = err.Error()
			return a, nil
		}
		a.afterDelete()
	case "n", "esc", "q":
		a.mode = modeMain
	}
	return a, nil
}

func (a *App) afterDelete() {
	a.sessions, _ = a.store.List()
	if a.sel >= len(a.sessions) {
		a.sel = len(a.sessions) - 1
	}
	if a.sel < 0 {
		a.sel = 0
	}
	a.mode = modeMain
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
		a.sessions, _ = a.store.List()
		for i, ss := range a.sessions {
			if ss.Name == s.Name {
				a.sel = i
				a.sessions[i] = s
			}
		}
		a.mode = modeMain
		a.focusList = false
		a.spawn(s)
	}
	return a, cmd
}

// --- View ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	selStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	runningDot    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("●")
	stoppedDot    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("○")
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	helpKeyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	listPaneStyle = lipgloss.NewStyle().Width(leftWidth).MaxWidth(leftWidth)
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

	left := a.viewList()
	right := a.viewTerminal()
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "│", right)
	return body + "\n" + a.viewStatusBar()
}

func (a *App) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(" claude-minimal") + "\n")
	b.WriteString(dimStyle.Render(" "+a.cfg.Root) + "\n\n")
	if len(a.sessions) == 0 {
		b.WriteString(dimStyle.Render(" no sessions — press ⌥n") + "\n")
	}
	for i, s := range a.sessions {
		dot := stoppedDot
		if s.Running() {
			dot = runningDot
		}
		name := s.Name
		if len(name) > leftWidth-5 {
			name = name[:leftWidth-6] + "…"
		}
		line := " " + dot + " " + name
		if i == a.sel {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
		if i == a.sel && len(s.Projects) > 0 {
			for _, p := range s.Projects {
				b.WriteString(dimStyle.Render("    ↳ "+p.Name) + "\n")
			}
		}
	}
	_, rows := a.termSize()
	content := b.String()
	lines := strings.Split(content, "\n")
	for len(lines) < rows {
		lines = append(lines, "")
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return listPaneStyle.Render(strings.Join(lines, "\n"))
}

func (a *App) viewTerminal() string {
	cols, rows := a.termSize()
	s := a.selected()
	if s == nil {
		return centered(cols, rows, dimStyle.Render("No session selected.\n\nPress ⌥n to create one."))
	}
	if s.Proc == nil {
		return centered(cols, rows, dimStyle.Render(
			fmt.Sprintf("Session %q is not running.\n\nPress enter to resume with previous context\n(claude --continue + %s)", s.Name, "context.md")))
	}
	out := RenderTerminal(s.Proc.VT, cols, rows, !a.focusList)
	if s.Proc.Exited() {
		lines := strings.Split(out, "\n")
		note := errStyle.Render(" [exited — press enter to restart] ")
		if len(lines) > 0 {
			lines[len(lines)-1] = note
		}
		out = strings.Join(lines, "\n")
	}
	return out
}

func centered(cols, rows int, text string) string {
	return lipgloss.Place(cols, rows, lipgloss.Center, lipgloss.Center, text)
}

func (a *App) viewStatusBar() string {
	help := []string{
		helpKeyStyle.Render("⌥n") + " new",
		helpKeyStyle.Render("⌥↑/↓") + " switch",
		helpKeyStyle.Render("⌥d") + " delete",
		helpKeyStyle.Render("⌥l") + " list",
		helpKeyStyle.Render("⌥q") + " quit",
	}
	bar := " " + strings.Join(help, dimStyle.Render(" · "))
	if a.focusList {
		bar += dimStyle.Render("  [list focus: j/k, enter]")
	}
	if a.errMsg != "" {
		bar += "  " + errStyle.Render(a.errMsg)
	}
	return bar
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
	b.WriteString("\n" + helpKeyStyle.Render("y") + " delete   " +
		helpKeyStyle.Render("f") + " force   " + helpKeyStyle.Render("esc") + " cancel\n")
	if a.confirmMsg != "" {
		b.WriteString("\n" + errStyle.Render(a.confirmMsg) + "\n")
	}
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, b.String())
}
