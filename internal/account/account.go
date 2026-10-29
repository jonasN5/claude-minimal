// Package account rotates sessions across several Claude logins so that one
// account hitting its usage limit doesn't stop work.
//
// Each account is a separate Claude Code credential slot, selected with
// CLAUDE_SECURESTORAGE_CONFIG_DIR. That variable only moves where the login
// is stored (a distinct keychain item); the config directory (~/.claude) stays
// shared, so transcripts, settings, hooks and `claude --continue` work the same
// whichever account a session runs on. Switching accounts therefore never
// loses conversation history.
package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultName is the account using Claude Code's standard credential slot —
// the login that already exists before any accounts are configured.
const DefaultName = "default"

// SlotEnv is the Claude Code variable selecting the credential slot.
const SlotEnv = "CLAUDE_SECURESTORAGE_CONFIG_DIR"

type state struct {
	Current string `json:"current"`
	// Exhausted maps an account to when its usage limit resets.
	Exhausted map[string]time.Time `json:"exhausted"`
}

// Pool is the configured account list plus the persisted rotation state
// (<data>/accounts/state.json), so a restart remembers which accounts are
// still limited.
type Pool struct {
	names []string
	dir   string
	st    state
}

// NewPool returns nil when no accounts are configured (rotation disabled).
func NewPool(dataDir string, names []string) *Pool {
	if len(names) == 0 {
		return nil
	}
	p := &Pool{names: names, dir: filepath.Join(dataDir, "accounts")}
	if data, err := os.ReadFile(p.statePath()); err == nil {
		_ = json.Unmarshal(data, &p.st)
	}
	if p.st.Exhausted == nil {
		p.st.Exhausted = map[string]time.Time{}
	}
	return p
}

func (p *Pool) statePath() string { return filepath.Join(p.dir, "state.json") }

func (p *Pool) save() {
	data, err := json.MarshalIndent(p.st, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(p.dir, 0o700)
	tmp := p.statePath() + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, p.statePath())
	}
}

// Names returns the configured accounts in rotation order.
func (p *Pool) Names() []string { return p.names }

// SlotDir is the directory whose hash names the account's keychain item
// ("" for the default slot).
func (p *Pool) SlotDir(name string) string {
	if name == DefaultName {
		return ""
	}
	return filepath.Join(p.dir, name)
}

// KeychainService is the macOS keychain item Claude Code stores the account's
// login under: "Claude Code-credentials", suffixed with a hash of the slot
// directory for non-default slots.
func (p *Pool) KeychainService(name string) string {
	const base = "Claude Code-credentials"
	if name == DefaultName {
		return base
	}
	sum := sha256.Sum256([]byte(p.SlotDir(name)))
	return base + "-" + hex.EncodeToString(sum[:])[:8]
}

// Env returns the environment selecting the account's credential slot.
func (p *Pool) Env(name string) []string {
	if name == DefaultName {
		return nil // inherit Claude Code's standard slot
	}
	dir := p.SlotDir(name)
	_ = os.MkdirAll(dir, 0o700)
	return []string{SlotEnv + "=" + dir}
}

// ExhaustedUntil reports when a limited account becomes usable again.
func (p *Pool) ExhaustedUntil(name string, now time.Time) (time.Time, bool) {
	until, ok := p.st.Exhausted[name]
	if !ok || !until.After(now) {
		return time.Time{}, false
	}
	return until, true
}

// Current returns the account new launches should use: the current one if it
// is not limited, otherwise the next available one in rotation order. When
// every account is limited it returns the one that resets first, and false.
func (p *Pool) Current(now time.Time) (string, bool) {
	start := 0
	for i, n := range p.names {
		if n == p.st.Current {
			start = i
			break
		}
	}
	for i := range p.names {
		n := p.names[(start+i)%len(p.names)]
		if _, limited := p.ExhaustedUntil(n, now); !limited {
			if n != p.st.Current {
				p.st.Current = n
				p.save()
			}
			return n, true
		}
	}
	first := p.names[0]
	for _, n := range p.names {
		if p.st.Exhausted[n].Before(p.st.Exhausted[first]) {
			first = n
		}
	}
	return first, false
}

// MarkExhausted records that name hit its usage limit until the given time.
func (p *Pool) MarkExhausted(name string, until time.Time) {
	p.st.Exhausted[name] = until
	p.save()
}

// NextReset is the earliest time a limited account becomes usable again.
func (p *Pool) NextReset(now time.Time) time.Time {
	var first time.Time
	for _, n := range p.names {
		if until, ok := p.ExhaustedUntil(n, now); ok && (first.IsZero() || until.Before(first)) {
			first = until
		}
	}
	return first
}
