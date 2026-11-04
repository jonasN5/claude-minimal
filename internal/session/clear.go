package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A session asks for a fresh conversation, the equivalent of /clear (which
// neither Claude nor a hook can run), by writing `../clear-requested` from its
// workspace. The file holds the first prompt of the new conversation. The app
// restarts Claude without --continue once the current turn has ended.

// DefaultClearPrompt starts the new conversation when the marker is empty.
const DefaultClearPrompt = "claude-minimal started this new conversation on request, to drop the previous one's accumulated context " +
	"(its tail is saved in ../context.md). Read ../progress.md first, then continue the work it describes."

// ClearFile is the marker a session writes to ask for a fresh conversation.
func (s *Session) ClearFile() string { return filepath.Join(s.Dir, "clear-requested") }

// ClearRequested reports whether the session asked for a fresh conversation.
func (s *Session) ClearRequested() bool {
	_, err := os.Stat(s.ClearFile())
	return err == nil
}

// TakeClearRequest consumes the marker and returns the prompt to start the
// new conversation with.
func (s *Session) TakeClearRequest() (string, bool) {
	data, err := os.ReadFile(s.ClearFile())
	if err != nil {
		return "", false
	}
	_ = os.Remove(s.ClearFile())
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		prompt = DefaultClearPrompt
	}
	return prompt, true
}

// turnQuiet is how long the transcript must stay unchanged after the final
// reply: Claude Code writes a message's blocks and its end-of-turn
// bookkeeping as separate lines.
const turnQuiet = 10 * time.Second

// TurnEnded reports whether Claude has finished its turn: the latest
// transcript's last message is a final reply (no tool call pending) and
// nothing has been written to it for turnQuiet.
func TurnEnded(workspace string, now time.Time) bool {
	line, mod := lastMessage(workspace)
	if line == nil || now.Sub(mod) < turnQuiet {
		return false
	}
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			StopReason string `json:"stop_reason"`
		} `json:"message"`
	}
	return json.Unmarshal(line, &entry) == nil && entry.Type == "assistant" && entry.Message.StopReason == "end_turn"
}
