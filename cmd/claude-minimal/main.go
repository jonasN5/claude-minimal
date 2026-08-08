// claude-minimal: a minimal, fast session manager for Claude Code.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonasN5/claude-minimal/internal/config"
	"github.com/jonasN5/claude-minimal/internal/shellenv"
	"github.com/jonasN5/claude-minimal/internal/ui"
)

func main() {
	root := flag.String("root", "", "projects root folder (default from config, else ~/projects)")
	flag.Parse()

	// Launched from a .app bundle, we inherit launchd's bare PATH; sessions
	// would then run setup hooks that can't find docker, npm or poetry.
	shellenv.Repair()

	cfg, err := config.Load(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}
	app, err := ui.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Mouse capture powers in-pane text selection (auto-copied on release)
	// and click-to-select in the session list.
	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	app.SetProgram(p)
	if _, err := p.Run(); err != nil {
		app.Shutdown()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
