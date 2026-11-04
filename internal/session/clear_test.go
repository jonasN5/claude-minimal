package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonasN5/claude-minimal/internal/config"
)

func TestLaunchArgvFresh(t *testing.T) {
	s := &Session{Meta: Meta{Name: "s", Started: true}, Dir: "/tmp/s"}
	cfg := &config.Config{ClaudeCmd: "claude"}
	script := s.LaunchArgv(cfg, "read it's progress", true)[2]
	if !strings.HasSuffix(script, `exec claude 'read it'\''s progress'`) || strings.Contains(script, "--continue") {
		t.Fatalf("script = %s", script)
	}
}

func TestTakeClearRequest(t *testing.T) {
	s := &Session{Dir: t.TempDir()}
	if s.ClearRequested() {
		t.Fatal("no marker: reported as requested")
	}
	if _, ok := s.TakeClearRequest(); ok {
		t.Fatal("no marker: taken")
	}
	for content, want := range map[string]string{"\n": DefaultClearPrompt, "  start over\n": "start over"} {
		if err := os.WriteFile(s.ClearFile(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if !s.ClearRequested() {
			t.Fatal("marker not seen")
		}
		if got, ok := s.TakeClearRequest(); !ok || got != want {
			t.Fatalf("marker %q: got %q %v, want %q", content, got, ok, want)
		}
		if s.ClearRequested() {
			t.Fatal("marker not consumed")
		}
	}
}

func TestTurnEnded(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfgDir)
	workspace := "/Users/x/.claude-minimal/sessions/s1/workspace"
	dir := filepath.Join(cfgDir, "projects", "-Users-x--claude-minimal-sessions-s1-workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if TurnEnded(workspace, now) {
		t.Fatal("no transcript: reported as ended")
	}
	toolUse := `{"type":"assistant","message":{"stop_reason":"tool_use","content":[{"type":"tool_use"}]}}` + "\n"
	result := `{"type":"user","message":{"content":[{"type":"tool_result"}]}}` + "\n"
	final := `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}` + "\n"
	meta := `{"type":"system","subtype":"turn_duration"}` + "\n" + `{"type":"last-prompt"}` + "\n"
	old := now.Add(-time.Minute)
	for _, c := range []struct {
		name, content string
		mod           time.Time
		want          bool
	}{
		{"tool call pending", toolUse, old, false},
		{"tool result not answered yet", toolUse + result, old, false},
		{"final reply, then bookkeeping", toolUse + result + final + meta, old, true},
		{"final reply just written", toolUse + result + final, now.Add(-2 * time.Second), false},
		{"user spoke after the reply", final + `{"type":"user","message":{"content":"next"}}` + "\n", old, false},
	} {
		p := filepath.Join(dir, "a.jsonl")
		if err := os.WriteFile(p, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, c.mod, c.mod); err != nil {
			t.Fatal(err)
		}
		if got := TurnEnded(workspace, now); got != c.want {
			t.Errorf("%s: TurnEnded = %v, want %v", c.name, got, c.want)
		}
	}
}
