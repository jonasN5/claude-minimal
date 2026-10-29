package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonasN5/claude-minimal/internal/config"
)

// A usage-limit entry as Claude Code writes it to the session transcript.
const limitLine = `{"type":"assistant","isApiErrorMessage":true,"error":"rate_limit","apiError":"usage_limit_reached","message":{"content":[{"type":"text","text":"You've hit your 5-hour limit · resets 3:40pm (Europe/Paris)"}]}}` + "\n"

func TestLimitWatchSeesOnlyNewEntries(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	workspace := "/Users/x/.claude-minimal/sessions/s1/workspace"
	dir := filepath.Join(cfgDir, "projects", "-Users-x--claude-minimal-sessions-s1-workspace")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "abc.jsonl")
	// An old limit hit, re-rendered on --continue, must not count.
	if err := os.WriteFile(transcript, []byte(limitLine), 0o644); err != nil {
		t.Fatal(err)
	}

	w := NewLimitWatch(workspace)
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	if _, hit := w.Poll(now); hit {
		t.Fatal("pre-existing entry reported")
	}

	appendTo := func(s string) {
		f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	appendTo(`{"type":"user","message":{"content":"mentions usage_limit_reached in text"}}` + "\n")
	if _, hit := w.Poll(now); hit {
		t.Fatal("non-error entry reported")
	}

	// A partly written line is not consumed until complete.
	appendTo(limitLine[:40])
	if _, hit := w.Poll(now); hit {
		t.Fatal("partial line reported")
	}
	appendTo(limitLine[40:])
	lim, hit := w.Poll(now)
	if !hit || !strings.Contains(lim.Message, "5-hour limit") {
		t.Fatalf("limit not reported: %+v %v", lim, hit)
	}
	if _, hit := w.Poll(now); hit {
		t.Fatal("same entry reported twice")
	}

	// Transcripts created after launch (a forked session) are read from the start.
	if err := os.WriteFile(filepath.Join(dir, "new.jsonl"), []byte(limitLine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit := w.Poll(now); !hit {
		t.Fatal("new transcript not watched")
	}
}

func TestParseReset(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	now := time.Date(2026, 10, 28, 11, 39, 0, 0, paris)
	cases := []struct {
		msg  string
		want time.Time
	}{
		{"You've hit your 5-hour limit · resets 3:40pm (Europe/Paris)", time.Date(2026, 10, 28, 15, 40, 0, 0, paris)},
		{"You've hit your 5-hour limit · resets 11am (Europe/Paris)", time.Date(2026, 10, 29, 11, 0, 0, 0, paris)},
		{"You've hit your 5-hour limit · resets 12am (Europe/Paris)", time.Date(2026, 10, 29, 0, 0, 0, 0, paris)},
		{"You've hit your weekly limit · resets Nov 2, 3pm (Europe/Paris)", time.Date(2026, 11, 2, 15, 0, 0, 0, paris)},
		{"You've hit your weekly limit · resets Nov 2 at 3pm (Europe/Paris)", time.Date(2026, 11, 2, 15, 0, 0, 0, paris)},
		{"You've hit your limit", now.Add(fallbackCooldown)},
	}
	for _, c := range cases {
		if got := ParseReset(c.msg, now); !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestLaunchArgvResumePrompt(t *testing.T) {
	s := &Session{Meta: Meta{Name: "s", Started: true}, Dir: "/tmp/s"}
	cfg := &config.Config{ClaudeCmd: "claude"}
	script := s.LaunchArgv(cfg, "it's your turn")[2]
	if !strings.HasSuffix(script, `exec claude --continue 'it'\''s your turn'`) {
		t.Fatalf("script = %s", script)
	}
}

func TestEnsureProgressNote(t *testing.T) {
	s := &Session{Dir: t.TempDir()}
	if err := os.MkdirAll(s.Workspace(), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Workspace(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Session: old\n\nuser notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.EnsureProgressNote(); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# Session: old\n\nuser notes\n") || strings.Count(string(data), "../progress.md") != 2 {
		t.Fatalf("CLAUDE.md = %q", data)
	}
}

func TestEndedOnLimit(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	workspace := "/Users/x/.claude-minimal/sessions/s1/workspace"
	dir := filepath.Join(cfgDir, "projects", "-Users-x--claude-minimal-sessions-s1-workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if EndedOnLimit(workspace) {
		t.Fatal("no transcript: reported as limited")
	}
	write := func(name, content string, mod time.Time) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	prompt := `{"type":"user","message":{"content":"fix it"}}` + "\n"
	meta := `{"type":"last-prompt","lastPrompt":"fix it"}` + "\n"
	old := time.Now().Add(-time.Hour)

	write("a.jsonl", prompt+limitLine+meta, old)
	if !EndedOnLimit(workspace) {
		t.Fatal("limit followed by metadata: not reported")
	}
	write("a.jsonl", limitLine+prompt, old)
	if EndedOnLimit(workspace) {
		t.Fatal("conversation went on after the limit: reported")
	}
	// Only the most recent transcript counts.
	write("a.jsonl", prompt+limitLine, old)
	write("b.jsonl", prompt, time.Now())
	if EndedOnLimit(workspace) {
		t.Fatal("older transcript's limit reported")
	}
}
