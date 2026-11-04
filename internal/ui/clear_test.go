package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jonasN5/claude-minimal/internal/config"
	"github.com/jonasN5/claude-minimal/internal/session"
)

// TestClearRequest drives checkClears: a session writes ../clear-requested,
// and once its turn has ended it is restarted on a new conversation (no
// --continue) with the marker's prompt.
func TestClearRequest(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp) // EnsureTrusted writes ~/.claude.json
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmp, ".claude"))

	// The fake Claude records its arguments, one launch per line.
	args := filepath.Join(tmp, "args")
	cfg := &config.Config{DataDir: filepath.Join(tmp, "data"),
		ClaudeCmd: `sh -c 'printf "%s|" "$@" >> "` + args + `"; echo >> "` + args + `"; exec sleep 30' claude`}
	s, err := session.NewStore(cfg).Create("orchestrator", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Started = true
	a := &App{cfg: cfg, store: session.NewStore(cfg), sessions: []*session.Session{s},
		width: 120, height: 40, notify: make(chan struct{}, 1), deleting: map[string]bool{}}
	t.Cleanup(func() {
		if s.Proc != nil {
			s.Proc.Kill()
		}
	})
	launches := func(n int) []string {
		t.Helper()
		var lines []string
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			data, _ := os.ReadFile(args)
			if lines = strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) >= n && lines[0] != "" {
				return lines
			}
		}
		t.Fatalf("want %d launches, got %q", n, lines)
		return nil
	}

	a.spawn(s)
	if got := launches(1); got[0] != "--continue|" {
		t.Fatalf("resume launch args = %q", got[0])
	}

	transcripts := filepath.Join(tmp, ".claude", "projects", regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(s.Workspace(), "-"))
	if err := os.MkdirAll(transcripts, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := func(content string, mod time.Time) {
		t.Helper()
		p := filepath.Join(transcripts, "t.jsonl")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	writeMarker := `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"Write"}]}}` + "\n"
	final := `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"cleared"}]}}` + "\n"
	old := time.Now().Add(-time.Minute)

	if err := os.WriteFile(s.ClearFile(), []byte("Read ../progress.md and go on.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := s.Proc

	transcript(writeMarker, old) // marker written, turn not over
	a.checkClears()
	transcript(writeMarker+final, time.Now()) // reply just written
	a.checkClears()
	if s.Proc != first || !s.ClearRequested() {
		t.Fatal("restarted before the turn ended")
	}

	transcript(writeMarker+final, old)
	a.checkClears()
	if s.Proc == first || !s.Running() || s.ClearRequested() {
		t.Fatalf("turn ended: restarted %v running %v marker left %v", s.Proc != first, s.Running(), s.ClearRequested())
	}
	if got := launches(2); got[1] != "Read ../progress.md and go on.|" {
		t.Fatalf("fresh launch args = %q", got[1])
	}

	second := s.Proc
	a.checkClears() // marker consumed: no further restart
	if s.Proc != second {
		t.Fatal("restarted again without a marker")
	}
}
