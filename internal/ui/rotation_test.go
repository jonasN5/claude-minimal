package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/jonasN5/claude-minimal/internal/account"
	"github.com/jonasN5/claude-minimal/internal/config"
	"github.com/jonasN5/claude-minimal/internal/session"
)

const limitLine = `{"type":"assistant","isApiErrorMessage":true,"apiError":"usage_limit_reached","message":{"content":[{"type":"text","text":"You've hit your 5-hour limit · resets 3:40pm (Europe/Paris)"}]}}` + "\n"

// TestRotationParksAndResumes drives checkLimits through a full night:
// limit → rotate, limit on the last account → park, reset → resume.
func TestRotationParksAndResumes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // EnsureTrusted writes ~/.claude.json
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))

	// "#" turns the --continue flag and prompt into a shell comment.
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "sleep 30 #", ResumePrompt: "go on"}
	s, err := session.NewStore(cfg).Create("night", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Started = true
	a := &App{cfg: cfg, store: session.NewStore(cfg), pool: account.NewPool(cfg.DataDir, []string{"a", "b"}),
		sessions: []*session.Session{s}, width: 120, height: 40, notify: make(chan struct{}, 1)}
	t.Cleanup(func() {
		if s.Proc != nil {
			s.Proc.Kill()
		}
	})

	transcripts := filepath.Join(tmp, ".claude", "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(s.Workspace(), "-"))
	if err := os.MkdirAll(transcripts, 0o755); err != nil {
		t.Fatal(err)
	}
	hitLimit := func() {
		t.Helper()
		f, err := os.OpenFile(filepath.Join(transcripts, "t.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(limitLine); err != nil {
			t.Fatal(err)
		}
	}

	a.spawn(s)
	if s.Account != "a" || !s.Running() {
		t.Fatalf("launch: account %q running %v (%s)", s.Account, s.Running(), a.errMsg)
	}

	// Creating or deleting another session reloads the list from disk; the
	// running session must keep its account and limit watch.
	a.reload()
	s = a.sessions[0]
	if s.Account != "a" || s.Limit == nil || !s.Running() {
		t.Fatalf("after reload: account %q watching %v running %v", s.Account, s.Limit != nil, s.Running())
	}

	hitLimit()
	a.checkLimits()
	if s.Account != "b" || !s.Running() || s.Parked {
		t.Fatalf("rotate: account %q running %v parked %v", s.Account, s.Running(), s.Parked)
	}

	hitLimit()
	a.checkLimits()
	if !s.Parked {
		t.Fatalf("all limited: session not parked (account %q)", s.Account)
	}
	a.checkLimits() // still limited: stays parked
	if !s.Parked {
		t.Fatal("unparked before any reset")
	}

	// Account a's limit passes.
	a.pool.MarkExhausted("a", time.Now().Add(-time.Second))
	a.checkLimits()
	if s.Parked || s.Account != "a" || !s.Running() || s.Limit == nil {
		t.Fatalf("after reset: parked %v account %q running %v watching %v", s.Parked, s.Account, s.Running(), s.Limit != nil)
	}
}

// TestRotationOnLimitMenu covers limits Claude Code enforces locally: no
// transcript entry, only the /rate-limit-options menu on screen.
func TestRotationOnLimitMenu(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))

	menu := `printf "  What do you want to do?\n\n  ❯ 1. Stop and wait for limit to reset\n    2. Wait here, then continue automatically at 3:40pm\n\n  Enter to confirm · Esc to cancel\n"`
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "sh -c '" + menu + "; sleep 30' #", ResumePrompt: "go on"}
	s, err := session.NewStore(cfg).Create("menu", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Started = true
	a := &App{cfg: cfg, store: session.NewStore(cfg), pool: account.NewPool(cfg.DataDir, []string{"a", "b"}),
		sessions: []*session.Session{s}, width: 120, height: 40, notify: make(chan struct{}, 1)}
	t.Cleanup(func() {
		if s.Proc != nil {
			s.Proc.Kill()
		}
	})

	a.spawn(s)
	for deadline := time.Now().Add(3 * time.Second); s.Account == "a" && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		a.checkLimits()
	}
	if s.Account != "b" || !s.Running() {
		t.Fatalf("menu: account %q running %v (%s)", s.Account, s.Running(), a.errMsg)
	}
	// Limited until the reset the menu names, plus the grace minute.
	if until, ok := a.pool.ExhaustedUntil("a", time.Now()); !ok || until.Local().Format("3:04pm") != "3:41pm" {
		t.Fatalf("a limited until %v (%v), want 3:41pm", until, ok)
	}
}

// limitedWhileTesting starts a session whose fake Claude runs a tool shell
// (a test suite) until the returned file exists, then makes it hit a limit
// and checks that it drains instead of restarting.
func limitedWhileTesting(t *testing.T) (a *App, s *session.Session, first *session.Proc, done string) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))
	marker := session.ToolShellMarker
	t.Cleanup(func() { session.ToolShellMarker = marker })
	session.ToolShellMarker = "fake-tool-shell-" + filepath.Base(tmp)

	done = filepath.Join(tmp, "tests-done")
	shell := `sh -c "while [ ! -f ` + done + ` ]; do sleep 0.1; done # ` + session.ToolShellMarker + `" & exec sleep 30`
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"), ClaudeCmd: "sh -c '" + shell + "' #", ResumePrompt: "go on"}
	s, err := session.NewStore(cfg).Create("tests", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Started = true
	a = &App{cfg: cfg, store: session.NewStore(cfg), pool: account.NewPool(cfg.DataDir, []string{"a", "b"}),
		sessions: []*session.Session{s}, width: 120, height: 40, notify: make(chan struct{}, 1)}
	t.Cleanup(func() {
		_ = os.WriteFile(done, nil, 0o644)
		if s.Proc != nil {
			s.Proc.Kill()
		}
	})

	a.spawn(s)
	first = s.Proc
	for deadline := time.Now().Add(3 * time.Second); session.Busy(s.Workspace(), first.Pid(), first.Started(), time.Now()) == ""; {
		if time.Now().After(deadline) {
			t.Fatal("tool shell never seen")
		}
		time.Sleep(50 * time.Millisecond)
	}

	transcripts := filepath.Join(tmp, ".claude", "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(s.Workspace(), "-"))
	if err := os.MkdirAll(transcripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(transcripts, "t.jsonl"), []byte(limitLine), 0o644); err != nil {
		t.Fatal(err)
	}

	a.checkLimits()
	a.checkLimits()
	if s.Proc != first || !s.Running() || s.Account != "a" || !s.Draining || s.Waiting != "1 shell" {
		t.Fatalf("restarted under running work: same proc %v running %v account %q draining %v waiting %q",
			s.Proc == first, s.Running(), s.Account, s.Draining, s.Waiting)
	}
	if _, limited := a.pool.ExhaustedUntil("a", time.Now()); !limited {
		t.Fatal("account a not marked limited while draining")
	}
	return a, s, first, done
}

// TestRotationWaitsForWork: a limit hit while a test suite runs restarts the
// session on the next account only once the suite has finished.
func TestRotationWaitsForWork(t *testing.T) {
	a, s, first, done := limitedWhileTesting(t)
	if err := os.WriteFile(done, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); s.Proc == first && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		a.checkLimits()
	}
	if s.Proc == first || s.Account != "b" || !s.Running() || s.Draining {
		t.Fatalf("after work done: restarted %v account %q running %v draining %v", s.Proc != first, s.Account, s.Running(), s.Draining)
	}
}

// TestDrainTimeout: work that never ends (a dev server) holds the restart
// for drainTimeout at most.
func TestDrainTimeout(t *testing.T) {
	a, s, first, _ := limitedWhileTesting(t)
	s.WaitSince = time.Now().Add(-drainTimeout)
	a.checkLimits()
	if s.Proc == first || s.Account != "b" || !s.Running() {
		t.Fatalf("after timeout: restarted %v account %q running %v", s.Proc != first, s.Account, s.Running())
	}
}
