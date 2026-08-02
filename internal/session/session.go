// Package session manages session state on disk: metadata, git worktrees,
// setup/teardown hooks and the generated workspace.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jonasN5/claude-minimal/internal/config"
)

// ProjectRef is a project attached to a session.
type ProjectRef struct {
	Name         string `json:"name"`
	RepoPath     string `json:"repo_path"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Base         string `json:"base,omitempty"` // commit the session branch started from
	Setup        string `json:"setup,omitempty"`
	Teardown     string `json:"teardown,omitempty"`
}

// Meta is the persisted session metadata (meta.json).
type Meta struct {
	Name      string       `json:"name"`
	CreatedAt time.Time    `json:"created_at"`
	Started   bool         `json:"started"` // true once Claude ran at least once
	Projects  []ProjectRef `json:"projects"`
}

// Session is a session on disk, optionally with a running process.
type Session struct {
	Meta
	Dir  string // <data>/sessions/<name>
	Proc *Proc  // nil when not running
}

// Workspace is the directory Claude runs in.
func (s *Session) Workspace() string { return filepath.Join(s.Dir, "workspace") }

// ContextFile is the persisted conversation tail, used to resume with context.
func (s *Session) ContextFile() string { return filepath.Join(s.Dir, "context.md") }

// Running reports whether the session has a live process.
func (s *Session) Running() bool { return s.Proc != nil && !s.Proc.Exited() }

func (s *Session) saveMeta() error {
	data, err := json.MarshalIndent(s.Meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, "meta.json"), data, 0o644)
}

// Store manages the sessions directory.
type Store struct {
	Cfg *config.Config
}

func NewStore(cfg *config.Config) *Store { return &Store{Cfg: cfg} }

func (st *Store) sessionsDir() string { return filepath.Join(st.Cfg.DataDir, "sessions") }

// List loads every session from disk, newest first.
func (st *Store) List() ([]*Session, error) {
	entries, err := os.ReadDir(st.sessionsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(st.sessionsDir(), e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			continue
		}
		s := &Session{Dir: dir}
		if err := json.Unmarshal(data, &s.Meta); err != nil {
			continue
		}
		out = append(out, s)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].CreatedAt.After(out[i].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

var nameRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// SanitizeName makes a string safe for directory and branch names.
func SanitizeName(name string) string {
	name = nameRe.ReplaceAllString(strings.TrimSpace(name), "-")
	return strings.Trim(name, "-.")
}

// Create builds a new session: directory, git worktrees, generated CLAUDE.md
// and setup script. Hooks are NOT run here — they stream inside the session
// pane when the process starts.
func (st *Store) Create(name string, projects []config.Project) (*Session, error) {
	name = SanitizeName(name)
	if name == "" {
		name = time.Now().Format("20060102-150405")
	}
	dir := filepath.Join(st.sessionsDir(), name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("session %q already exists", name)
	}
	s := &Session{
		Meta: Meta{Name: name, CreatedAt: time.Now()},
		Dir:  dir,
	}
	if err := os.MkdirAll(s.Workspace(), 0o755); err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }

	for _, p := range projects {
		ref := ProjectRef{
			Name:     p.Name,
			RepoPath: p.Path,
			Setup:    p.Setup,
			Teardown: p.Teardown,
		}
		if p.UseWorktree() {
			ref.Branch = "session/" + name
			ref.WorktreePath = filepath.Join(s.Workspace(), p.Name)
			cmd := exec.Command("git", "-C", p.Path, "worktree", "add", "-b", ref.Branch, ref.WorktreePath)
			if out, err := cmd.CombinedOutput(); err != nil {
				cleanup()
				return nil, fmt.Errorf("worktree for %s: %v\n%s", p.Name, err, out)
			}
			if base, err := exec.Command("git", "-C", ref.WorktreePath, "rev-parse", "HEAD").Output(); err == nil {
				ref.Base = strings.TrimSpace(string(base))
			}
		} else {
			// No isolation requested: symlink the repo into the workspace.
			ref.WorktreePath = filepath.Join(s.Workspace(), p.Name)
			if err := os.Symlink(p.Path, ref.WorktreePath); err != nil {
				cleanup()
				return nil, err
			}
		}
		s.Projects = append(s.Projects, ref)
	}

	if err := s.writeClaudeMD(); err != nil {
		cleanup()
		return nil, err
	}
	if err := s.writeSetupScript(); err != nil {
		cleanup()
		return nil, err
	}
	if err := s.saveMeta(); err != nil {
		cleanup()
		return nil, err
	}
	return s, nil
}

func (s *Session) writeClaudeMD() error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session: %s\n\n", s.Name)
	if len(s.Projects) == 0 {
		b.WriteString("Free-form session with no project attached. Use this directory as scratch space.\n")
	} else {
		b.WriteString("This workspace combines the following project checkouts. Read each project's own CLAUDE.md before working in it:\n\n")
		for _, p := range s.Projects {
			fmt.Fprintf(&b, "- `%s/` — worktree of `%s`", p.Name, p.RepoPath)
			if p.Branch != "" {
				fmt.Fprintf(&b, " (branch `%s`)", p.Branch)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nIf this session was resumed and earlier context is missing, the tail of the previous conversation is saved at `../context.md`.\n")
	return os.WriteFile(filepath.Join(s.Workspace(), "CLAUDE.md"), []byte(b.String()), 0o644)
}

func (s *Session) writeSetupScript() error {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "echo '[claude-minimal] provisioning session %s'\n", s.Name)
	for _, p := range s.Projects {
		if p.Setup == "" {
			continue
		}
		fmt.Fprintf(&b, "echo '[claude-minimal] %s: running setup hook'\n", p.Name)
		fmt.Fprintf(&b, "( cd %q && CLAUDE_MINIMAL_SESSION=%q %s ) || echo '[claude-minimal] WARNING: setup hook for %s failed'\n",
			p.WorktreePath, s.Name, p.Setup, p.Name)
	}
	b.WriteString("echo '[claude-minimal] provisioning done'\n")
	return os.WriteFile(filepath.Join(s.Dir, "setup.sh"), []byte(b.String()), 0o755)
}

// LaunchArgv returns the command started in the session PTY. First launch runs
// setup hooks then execs Claude; later launches resume the conversation.
func (s *Session) LaunchArgv(cfg *config.Config) []string {
	claude := cfg.ClaudeCmd
	for _, a := range cfg.ClaudeArgs {
		claude += " " + shQuote(a)
	}
	var script string
	if !s.Started {
		script = fmt.Sprintf("sh %s; exec %s", shQuote(filepath.Join(s.Dir, "setup.sh")), claude)
	} else {
		script = fmt.Sprintf("exec %s --continue", claude)
	}
	return []string{"/bin/sh", "-c", script}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// MarkStarted records that Claude ran, so the next launch uses --continue.
func (s *Session) MarkStarted() {
	if !s.Started {
		s.Started = true
		_ = s.saveMeta()
	}
}

// DirtyProjects returns names of worktrees holding work that would be lost on
// deletion: uncommitted changes, or commits not present on any remote.
func (s *Session) DirtyProjects() []string {
	var dirty []string
	for _, p := range s.Projects {
		if p.Branch == "" {
			continue
		}
		out, err := exec.Command("git", "-C", p.WorktreePath, "status", "--porcelain").Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			dirty = append(dirty, p.Name)
			continue
		}
		if p.Base == "" {
			continue
		}
		// Commits made in this session that are on neither the base nor any remote.
		out, err = exec.Command("git", "-C", p.WorktreePath, "rev-list", "--count", p.Branch, "--not", p.Base, "--remotes").Output()
		if err == nil && strings.TrimSpace(string(out)) != "0" {
			dirty = append(dirty, p.Name)
		}
	}
	return dirty
}

// Delete kills the process, runs teardown hooks, removes worktrees and the
// session directory. Unless force is set, it refuses if a worktree is dirty.
func (st *Store) Delete(s *Session, force bool) error {
	if !force {
		if dirty := s.DirtyProjects(); len(dirty) > 0 {
			return fmt.Errorf("uncommitted changes in: %s", strings.Join(dirty, ", "))
		}
	}
	if s.Proc != nil {
		s.Proc.Kill()
	}
	logPath := filepath.Join(s.Dir, "teardown.log")
	logFile, _ := os.Create(logPath)
	for _, p := range s.Projects {
		if p.Teardown != "" {
			cmd := exec.Command("/bin/sh", "-c", p.Teardown)
			cmd.Dir = p.WorktreePath
			cmd.Env = append(os.Environ(), "CLAUDE_MINIMAL_SESSION="+s.Name)
			cmd.Stdout, cmd.Stderr = logFile, logFile
			_ = cmd.Run() // best effort; logged
		}
		if p.Branch != "" {
			_ = exec.Command("git", "-C", p.RepoPath, "worktree", "remove", "--force", p.WorktreePath).Run()
			_ = exec.Command("git", "-C", p.RepoPath, "branch", "-D", p.Branch).Run()
		}
	}
	if logFile != nil {
		logFile.Close()
	}
	return os.RemoveAll(s.Dir)
}
