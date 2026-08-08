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

	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true"}
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

	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true"}
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
	p, err := Start(tmp, []string{"/bin/sh", "-c", "printf 'hello \\033[31mworld\\033[0m\\r\\n'; sleep 0.3"}, 80, 24, tail,
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

func TestEnsureTrusted(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "claude.json")
	if err := os.WriteFile(cfgPath, []byte(`{"numStartups":42,"projects":{"/existing":{"hasTrustDialogAccepted":true,"history":[1,2]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureTrustedIn(cfgPath, "/new/workspace"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	s := string(data)
	for _, want := range []string{`"/new/workspace":{"hasTrustDialogAccepted":true}`, `"numStartups":42`, `"history":[1,2]`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	// Idempotent, and refuses to rewrite an unparseable config.
	if err := ensureTrustedIn(cfgPath, "/new/workspace"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureTrustedIn(cfgPath, "/other"); err == nil {
		t.Fatal("expected parse error on corrupt config")
	}
	if data, _ := os.ReadFile(cfgPath); string(data) != "{corrupt" {
		t.Error("corrupt config was rewritten")
	}
}

// bareRepoWithClone builds an "upstream" repo plus a clone whose local HEAD is
// deliberately left behind upstream — the shape a shared checkout has when the
// user last pulled weeks ago.
func bareRepoWithClone(t *testing.T, tmp string) (clone, upstreamTip, staleHEAD string) {
	t.Helper()
	upstream := filepath.Join(tmp, "upstream.git")
	work := filepath.Join(tmp, "work")
	clone = filepath.Join(tmp, "clone")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "init", "--bare", "-b", "master", upstream).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	gitRepo(t, work)
	git(work, "remote", "add", "origin", upstream)
	git(work, "push", "-q", "origin", "master")
	if out, err := exec.Command("git", "clone", "-q", upstream, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	staleHEAD = git(clone, "rev-parse", "HEAD")
	// Upstream moves on; the clone never pulls, so its HEAD and its cached
	// origin/master both stay at staleHEAD until something fetches.
	git(work, "commit", "--allow-empty", "-m", "newer upstream work")
	git(work, "push", "-q", "origin", "master")
	upstreamTip = git(work, "rev-parse", "HEAD")
	if upstreamTip == staleHEAD {
		t.Fatal("fixture broken: upstream did not advance")
	}
	return clone, upstreamTip, staleHEAD
}

func TestCreateBranchesFromFreshUpstream(t *testing.T) {
	tmp := t.TempDir()
	clone, upstreamTip, staleHEAD := bareRepoWithClone(t, tmp)

	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true"}
	s, err := NewStore(cfg).Create("fresh", []config.Project{{Name: "repo", Path: clone}})
	if err != nil {
		t.Fatal(err)
	}

	// The upstream tip is read from the upstream repo itself, so the assertion
	// does not depend on the clone having fetched — which is the very thing
	// under test.
	switch s.Projects[0].Base {
	case upstreamTip:
	case staleHEAD:
		t.Errorf("session branched from the shared checkout's stale HEAD %s; want fresh upstream tip %s", staleHEAD, upstreamTip)
	default:
		t.Errorf("session base = %s, want fresh upstream tip %s", s.Projects[0].Base, upstreamTip)
	}
}

func TestFreshBaseRefWithoutRemote(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)
	// No remote: caller must fall back to git's own default (branch from HEAD).
	if base := freshBaseRef(repo); base != "" {
		t.Errorf("freshBaseRef without remote = %q, want empty", base)
	}
	// ...and Create must still succeed.
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true"}
	if _, err := NewStore(cfg).Create("noremote", []config.Project{{Name: "repo", Path: repo}}); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchArgvExportsSessionName(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRepo(t, repo)
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "true"}
	s, err := NewStore(cfg).Create("port-test", []config.Project{{Name: "repo", Path: repo}})
	if err != nil {
		t.Fatal(err)
	}
	// Project hooks derive per-session ports from this; it must be set on the
	// first launch and on every resumed launch, or the two disagree.
	for _, started := range []bool{false, true} {
		s.Started = started
		script := s.LaunchArgv(cfg)[2]
		if !strings.Contains(script, "CLAUDE_MINIMAL_SESSION='port-test'") {
			t.Errorf("started=%v: session name not exported: %s", started, script)
		}
		if !strings.Contains(script, "export CLAUDE_MINIMAL_SESSION") {
			t.Errorf("started=%v: session name not exported to children: %s", started, script)
		}
	}
}
