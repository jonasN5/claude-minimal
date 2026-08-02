package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonasN5/claude-minimal/internal/config"
)

func gitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "master"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestCreateAndDelete(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)

	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true", TailLines: 100}
	st := NewStore(cfg)
	s, err := st.Create("test one", []config.Project{{Name: "repo", Path: repo, Setup: "echo hook-ran"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "test-one" {
		t.Errorf("name = %q", s.Name)
	}
	if _, err := os.Stat(filepath.Join(s.Workspace(), "repo", ".git")); err != nil {
		t.Error("worktree missing:", err)
	}
	claudeMD, _ := os.ReadFile(filepath.Join(s.Workspace(), "CLAUDE.md"))
	if !strings.Contains(string(claudeMD), "repo/") {
		t.Error("CLAUDE.md missing project reference")
	}

	// First launch runs setup then claude; second uses --continue.
	argv := s.LaunchArgv(cfg)
	if !strings.Contains(argv[2], "setup.sh") {
		t.Errorf("first launch should run setup: %v", argv)
	}
	s.MarkStarted()
	argv = s.LaunchArgv(cfg)
	if !strings.Contains(argv[2], "--continue") {
		t.Errorf("second launch should resume: %v", argv)
	}

	sessions, err := st.List()
	if err != nil || len(sessions) != 1 {
		t.Fatalf("list: %v %v", sessions, err)
	}

	if err := st.Delete(s, false); err != nil {
		t.Fatal("delete:", err)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Error("session dir still exists")
	}
	out, _ := exec.Command("git", "-C", repo, "branch", "--list", "session/test-one").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Error("session branch not cleaned up")
	}
}

func TestDeleteRefusesDirty(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)

	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true", TailLines: 100}
	st := NewStore(cfg)
	s, err := st.Create("dirty", []config.Project{{Name: "repo", Path: repo}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Workspace(), "repo", "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(s, false); err == nil {
		t.Fatal("expected dirty refusal")
	}
	if err := st.Delete(s, true); err != nil {
		t.Fatal("force delete:", err)
	}
}

func TestProcTail(t *testing.T) {
	tmp := t.TempDir()
	tail := filepath.Join(tmp, "context.md")
	updates := make(chan struct{}, 100)
	// The brief sleep keeps the PTY open long enough for the reader to drain
	// (macOS can drop buffered PTY output when the child exits instantly);
	// real sessions are long-lived interactive processes.
	p, err := Start(tmp, []string{"/bin/sh", "-c", "printf 'hello \\033[31mworld\\033[0m\\r\\n'; sleep 0.3"}, 80, 24, tail, 100,
		func() { updates <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for !p.Exited() {
		select {
		case <-updates:
		case <-deadline:
			t.Fatal("process never exited")
		}
	}
	data, err := os.ReadFile(tail)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Errorf("tail missing ANSI-stripped output:\n%s", data)
	}
}
