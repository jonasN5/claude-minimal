package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentWorking(t *testing.T) {
	const (
		final    = `{"type":"assistant","message":{"stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`
		limited  = `{"type":"assistant","isApiErrorMessage":true,"apiError":"usage_limit_reached","message":{"stop_reason":"stop_sequence","content":[{"type":"text","text":"You've hit your limit"}]}}`
		toolCall = `{"type":"assistant","message":{"stop_reason":null,"content":[{"type":"tool_use","name":"Bash"}]}}`
		result   = `{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}`
	)
	for _, c := range []struct {
		name string
		line string
		idle time.Duration
		want bool
	}{
		{"final reply", final, time.Second, false},
		{"stopped on limit", limited, time.Second, false},
		{"tool running, quiet for long", toolCall, time.Hour, true},
		{"awaiting API", result, time.Minute, true},
		{"gone quiet", result, agentQuiet + time.Second, false},
	} {
		if got := agentWorking([]byte(c.line), c.idle); got != c.want {
			t.Errorf("%s: working = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestActiveAgentsIgnoresEarlierRuns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", tmp)
	ws := "/x/workspace"
	dir := filepath.Join(transcriptDir(ws), "sid", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toolCall := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash"}]}}` + "\n"
	now := time.Now()
	started := now.Add(-10 * time.Minute)
	write := func(name string, mod time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(toolCall), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("agent-old.jsonl", started.Add(-time.Hour)) // killed by an earlier restart
	if n := activeAgents(ws, started, now); n != 0 {
		t.Fatalf("earlier run's agent counted: %d", n)
	}
	write("agent-new.jsonl", started.Add(time.Minute))
	if n := activeAgents(ws, started, now); n != 1 {
		t.Fatalf("active agents = %d, want 1", n)
	}
}
