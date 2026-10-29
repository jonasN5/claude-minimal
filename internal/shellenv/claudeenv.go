package shellenv

import (
	"os"
	"strings"
)

// parentClaudeVars are set by a running Claude Code for the commands it
// spawns. When claude-minimal is started from inside a Claude session (a
// restart run from a session's Bash tool, or `open -a` from one, which passes
// the caller's environment to the app), they leak into every session it
// launches: each Claude then believes it is a child of that session, turns
// transcript saving off (breaking --continue and usage-limit detection) and
// shares the parent's session id and messaging socket.
var parentClaudeVars = []string{
	"CLAUDECODE",
	"CLAUDE_PID",
	"CLAUDE_EFFORT",
	"CLAUDE_MINIMAL_SESSION",
}

// StripParentClaude removes a parent Claude Code session's markers from the
// environment and returns the names it removed. User configuration such as
// CLAUDE_CONFIG_DIR is kept; only per-session CLAUDE_CODE_* state is dropped.
func StripParentClaude() []string {
	var removed []string
	unset := func(name string) {
		if _, ok := os.LookupEnv(name); ok {
			os.Unsetenv(name)
			removed = append(removed, name)
		}
	}
	for _, name := range parentClaudeVars {
		unset(name)
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CLAUDE_CODE_") && isSessionScoped(name) {
			unset(name)
		}
	}
	return removed
}

// isSessionScoped reports whether a CLAUDE_CODE_* variable describes the
// parent session rather than user configuration (e.g. CLAUDE_CODE_MAX_* or
// CLAUDE_CODE_USE_BEDROCK, which must survive).
func isSessionScoped(name string) bool {
	switch name {
	case "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_CHILD_SESSION":
		return true
	}
	for _, prefix := range []string{"CLAUDE_CODE_SESSION_", "CLAUDE_CODE_MESSAGING_"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
