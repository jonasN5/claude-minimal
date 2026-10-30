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
