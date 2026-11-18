package session

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Restarting Claude kills everything it runs in-process: subagents and the
// shell commands of every agent (test suites, builds, background tasks). A
// restart waits until that work is done, so it isn't lost.

// ToolShellMarker appears in the command line of every shell Claude Code
// starts for a Bash tool call (foreground or background, main thread or
// subagent): `zsh -c source ~/.claude/shell-snapshots/snapshot-….sh && …`.
// MCP servers and other helpers don't carry it. A variable for tests.
var ToolShellMarker = "/shell-snapshots/"

// agentQuiet is how long a subagent transcript may go unwritten before the
// agent counts as stopped. Completed subagents usually don't log their final
// reply, so their transcript just goes quiet; one waiting on an API response
// (long thinking) is quiet too, for at most a few minutes.
const agentQuiet = 3 * time.Minute

// Busy describes the work in flight in a session's Claude process (pid,
// launched at started), e.g. "2 shells, 1 agent", or "" when it can be
// restarted without cutting anything off.
func Busy(workspace string, pid int, started, now time.Time) string {
	var parts []string
	if n := toolShells(pid); n > 0 {
		parts = append(parts, plural(n, "shell"))
	}
	if n := activeAgents(workspace, started, now); n > 0 {
		parts = append(parts, plural(n, "agent"))
	}
	return strings.Join(parts, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// toolShells counts tool shells that are direct children of pid. It parses
// ps rather than using pgrep, which silently skips its own ancestors.
func toolShells(pid int) int {
	out, err := exec.Command("ps", "-axo", "ppid=,command=").Output()
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		ppid, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && ppid == strconv.Itoa(pid) && strings.Contains(cmd, ToolShellMarker) {
			n++
		}
	}
	return n
}

// activeAgents counts subagents of this process that are still working.
// Subagent transcripts live in <transcripts>/<session id>/subagents/; those
// untouched since the process started belong to an earlier run.
func activeAgents(workspace string, started, now time.Time) int {
	paths, _ := filepath.Glob(filepath.Join(transcriptDir(workspace), "*", "subagents", "*.jsonl"))
	n := 0
	for _, p := range paths {
		line, mod := lastEntry(p)
		if line == nil || mod.Before(started) {
			continue
		}
		if agentWorking(line, now.Sub(mod)) {
			n++
		}
	}
	return n
}

// agentWorking classifies a subagent from its last transcript entry and the
// time since it was written.
func agentWorking(line []byte, idle time.Duration) bool {
	var entry struct {
		Type              string `json:"type"`
		IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
		Message           struct {
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return false
	}
	if entry.Type == "assistant" {
		if entry.IsAPIErrorMessage || entry.Message.StopReason == "end_turn" {
			return false // stopped on an error (e.g. the usage limit) or done
		}
		var blocks []struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(entry.Message.Content, &blocks)
		for _, b := range blocks {
			if b.Type == "tool_use" {
				// A tool is running: a long one (tests via Bash, a web
				// fetch, a nested agent) leaves the transcript quiet.
				return true
			}
		}
	}
	// Waiting on the API, or finished without logging its final reply.
	return idle < agentQuiet
}
