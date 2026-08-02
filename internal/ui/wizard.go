package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonasN5/claude-minimal/internal/config"
)

// wizard is the two-step new-session flow: name, then project multi-select.
type wizard struct {
	step     int // 0 = name, 1 = projects
	name     textinput.Model
	projects []config.Project
	selected map[int]bool
	cursor   int
	errMsg   string
}

func newWizard(cfg *config.Config) *wizard {
	ti := textinput.New()
	ti.Placeholder = "session name (empty = timestamp)"
	ti.CharLimit = 60
	ti.Width = 40
	return &wizard{
		name:     ti,
		projects: cfg.DiscoverProjects(),
		selected: map[int]bool{},
	}
}

func (w *wizard) chosen() []config.Project {
	var out []config.Project
	for i, p := range w.projects {
		if w.selected[i] {
			out = append(out, p)
		}
	}
	return out
}

// update returns (done, cancelled, cmd).
func (w *wizard) update(msg tea.KeyMsg) (bool, bool, tea.Cmd) {
	if msg.String() == "esc" {
		if w.step == 1 {
			w.step = 0
			return false, false, w.name.Focus()
		}
		return false, true, nil
	}
	if w.step == 0 {
		if msg.String() == "enter" {
			w.step = 1
			w.name.Blur()
			return false, false, nil
		}
		var cmd tea.Cmd
		w.name, cmd = w.name.Update(msg)
		return false, false, cmd
	}
	switch msg.String() {
	case "up", "k":
		if w.cursor > 0 {
			w.cursor--
		}
	case "down", "j":
		if w.cursor < len(w.projects)-1 {
			w.cursor++
		}
	case " ", "space", "tab":
		w.selected[w.cursor] = !w.selected[w.cursor]
	case "enter":
		return true, false, nil
	}
	return false, false, nil
}

func (w *wizard) view(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New session") + "\n\n")
	if w.step == 0 {
		b.WriteString("Name:\n" + w.name.View() + "\n\n")
		b.WriteString(dimStyle.Render("enter: next · esc: cancel"))
	} else {
		b.WriteString("Project context to load (space to toggle, none = free-form):\n\n")
		for i, p := range w.projects {
			check := "[ ]"
			if w.selected[i] {
				check = "[x]"
			}
			line := check + " " + p.Name
			if p.Setup != "" {
				line += dimStyle.Render("  (setup hook)")
			}
			if i == w.cursor {
				line = selStyle.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("enter: create · esc: back"))
	}
	if w.errMsg != "" {
		b.WriteString("\n\n" + errStyle.Render(w.errMsg))
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, b.String())
}
