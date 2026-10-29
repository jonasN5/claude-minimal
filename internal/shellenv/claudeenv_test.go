package shellenv

import (
	"os"
	"testing"
)

func TestStripParentClaude(t *testing.T) {
	for k, v := range map[string]string{
		"CLAUDECODE":                   "1",
		"CLAUDE_CODE_CHILD_SESSION":    "1",
		"CLAUDE_CODE_SESSION_ID":       "abc",
		"CLAUDE_CODE_SESSION_ATTENDED": "1",
		"CLAUDE_CODE_MESSAGING_SOCKET": "/tmp/x.sock",
		"CLAUDE_CODE_ENTRYPOINT":       "cli",
		"CLAUDE_MINIMAL_SESSION":       "other",
		"CLAUDE_CODE_MAX_WEB_SEARCHES": "12000",
		"CLAUDE_CONFIG_DIR":            "/cfg",
		"CLAUDE_CODE_USE_BEDROCK":      "1",
	} {
		t.Setenv(k, v)
	}
	StripParentClaude()
	for _, gone := range []string{"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_SESSION_ATTENDED", "CLAUDE_CODE_MESSAGING_SOCKET", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_MINIMAL_SESSION"} {
		if _, ok := os.LookupEnv(gone); ok {
			t.Errorf("%s not removed", gone)
		}
	}
	for _, kept := range []string{"CLAUDE_CODE_MAX_WEB_SEARCHES", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_USE_BEDROCK"} {
		if _, ok := os.LookupEnv(kept); !ok {
			t.Errorf("%s removed", kept)
		}
	}
}
