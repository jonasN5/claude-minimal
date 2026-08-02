// Package config loads ~/.config/claude-minimal/config.toml and discovers
// git repositories under the configured root folder.
package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Project describes one repository that can be attached to a session.
type Project struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
	// Worktree defaults to true: sessions get an isolated git worktree.
	Worktree *bool `toml:"worktree"`
	// Setup is a shell command run inside the worktree before Claude starts
	// (e.g. a script that clones the DB and assigns ports). Its output
	// streams into the session pane.
	Setup string `toml:"setup"`
	// Teardown is a shell command run inside the worktree when the session
	// is deleted (e.g. stop containers, drop the cloned DB).
	Teardown string `toml:"teardown"`
}

// UseWorktree reports whether sessions should create a git worktree.
func (p Project) UseWorktree() bool { return p.Worktree == nil || *p.Worktree }

// Config is the on-disk configuration.
type Config struct {
	// Root is the folder scanned for git repositories (default ~/projects).
	Root string `toml:"root"`
	// AutoDiscover controls whether git repositories under Root are added to
	// the project picker alongside configured [[project]] entries (default
	// true). Set to false to offer only the configured projects.
	AutoDiscover *bool `toml:"auto_discover"`
	// ScanDepth is how many directory levels below Root to scan (default 2).
	ScanDepth int `toml:"scan_depth"`
	// ClaudeCmd is the binary launched in each session (default "claude").
	ClaudeCmd string `toml:"claude_cmd"`
	// ClaudeArgs are extra arguments appended to every launch.
	ClaudeArgs []string `toml:"claude_args"`
	// DataDir holds session state (default ~/.claude-minimal).
	DataDir string `toml:"data_dir"`
	// TailLines is how many lines of conversation tail are persisted to the
	// session context file (default 2000).
	TailLines int `toml:"tail_lines"`

	Projects []Project `toml:"project"`
}

// Path returns the config file location.
func Path() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "claude-minimal", "config.toml")
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// Load reads the config file, applying defaults for anything unset.
func Load(rootOverride string) (*Config, error) {
	cfg := &Config{}
	if data, err := os.ReadFile(Path()); err == nil {
		if err := toml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	home, _ := os.UserHomeDir()
	if rootOverride != "" {
		cfg.Root = rootOverride
	}
	if cfg.Root == "" {
		cfg.Root = filepath.Join(home, "projects")
	}
	cfg.Root = expand(cfg.Root)
	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 2
	}
	if cfg.ClaudeCmd == "" {
		cfg.ClaudeCmd = "claude"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = filepath.Join(home, ".claude-minimal")
	}
	cfg.DataDir = expand(cfg.DataDir)
	if cfg.TailLines <= 0 {
		cfg.TailLines = 2000
	}
	for i := range cfg.Projects {
		cfg.Projects[i].Path = expand(cfg.Projects[i].Path)
		if cfg.Projects[i].Name == "" {
			cfg.Projects[i].Name = filepath.Base(cfg.Projects[i].Path)
		}
	}
	return cfg, nil
}

// DiscoverProjects returns configured projects plus every git repository found
// under Root (up to ScanDepth levels deep) that is not already configured.
func (c *Config) DiscoverProjects() []Project {
	seen := map[string]bool{}
	out := make([]Project, 0, len(c.Projects))
	for _, p := range c.Projects {
		seen[p.Path] = true
		out = append(out, p)
	}
	if c.AutoDiscover != nil && !*c.AutoDiscover {
		return out
	}
	var discovered []Project
	var scan func(dir string, depth int)
	scan = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				if !seen[path] {
					seen[path] = true
					discovered = append(discovered, Project{Name: e.Name(), Path: path})
				}
				continue
			}
			if depth > 1 {
				scan(path, depth-1)
			}
		}
	}
	scan(c.Root, c.ScanDepth)
	sort.Slice(discovered, func(i, j int) bool { return discovered[i].Name < discovered[j].Name })
	return append(out, discovered...)
}
