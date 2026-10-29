package session

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Limit is a usage-limit error Claude Code recorded in a session transcript.
type Limit struct {
	Message string    // e.g. "You've hit your 5-hour limit · resets 3:40pm (Europe/Paris)"
	Resets  time.Time // when the account is usable again
}

// LimitWatch tails a session's Claude Code transcripts for usage-limit errors.
//
// It reads the structured transcript rather than scraping the screen: on
// `--continue` Claude re-renders the old conversation, including any earlier
// limit message, which a screen scraper would mistake for a fresh hit. Only
// bytes appended after the watch was created are considered.
type LimitWatch struct {
	dir     string
	offsets map[string]int64
}

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// transcriptDir mirrors Claude Code's layout: <config>/projects/<cwd with
// every non-alphanumeric character replaced by '-'>.
func transcriptDir(workspace string) string {
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfgDir == "" {
		home, _ := os.UserHomeDir()
		cfgDir = filepath.Join(home, ".claude")
	}
	return filepath.Join(cfgDir, "projects", nonAlnum.ReplaceAllString(workspace, "-"))
}

// NewLimitWatch starts watching from the transcripts' current ends.
func NewLimitWatch(workspace string) *LimitWatch {
	w := &LimitWatch{dir: transcriptDir(workspace), offsets: map[string]int64{}}
	w.scan(func(name string, size int64) { w.offsets[name] = size })
	return w
}

func (w *LimitWatch) scan(fn func(name string, size int64)) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		// Top-level files only: subagent transcripts repeat the main
		// session's limit error.
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil {
			fn(e.Name(), info.Size())
		}
	}
}

// Poll returns the first usage-limit error appended since the last poll.
func (w *LimitWatch) Poll(now time.Time) (Limit, bool) {
	var found Limit
	var hit bool
	w.scan(func(name string, size int64) {
		off := w.offsets[name] // new files start at 0: all of their content is new
		if size < off {
			off = size // truncated or replaced
		}
		if size == off {
			w.offsets[name] = off
			return
		}
		f, err := os.Open(filepath.Join(w.dir, name))
		if err != nil {
			return
		}
		defer f.Close()
		buf, err := io.ReadAll(io.NewSectionReader(f, off, size-off))
		if err != nil {
			return
		}
		// Consume complete lines only; a partly written line is re-read next poll.
		end := bytes.LastIndexByte(buf, '\n') + 1
		w.offsets[name] = off + int64(end)
		if hit {
			return
		}
		for _, line := range bytes.Split(buf[:end], []byte("\n")) {
			if msg, ok := limitMessage(line); ok {
				found, hit = Limit{Message: msg, Resets: ParseReset(msg, now)}, true
				return
			}
		}
	})
	return found, hit
}

// EndedOnLimit reports whether the session's latest transcript ends on a
// usage-limit error, i.e. Claude stopped mid-task waiting for a reset. It
// lets a restarted app re-park sessions whose in-memory parking was lost.
func EndedOnLimit(workspace string) bool {
	dir := transcriptDir(workspace)
	var latest string
	var latestMod time.Time
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(latestMod) {
			latest, latestMod = e.Name(), info.ModTime()
		}
	}
	if latest == "" {
		return false
	}
	f, err := os.Open(filepath.Join(dir, latest))
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	const tail = 256 << 10
	off := max(info.Size()-tail, 0)
	buf, err := io.ReadAll(io.NewSectionReader(f, off, info.Size()-off))
	if err != nil {
		return false
	}
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(lines[i], &entry) != nil || (entry.Type != "user" && entry.Type != "assistant") {
			continue // partial first line, metadata, summaries
		}
		_, ok := limitMessage(lines[i])
		return ok
	}
	return false
}

func limitMessage(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte("usage_limit_reached")) {
		return "", false
	}
	var entry struct {
		Type              string `json:"type"`
		IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
		APIError          string `json:"apiError"`
		Message           struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "assistant" ||
		!entry.IsAPIErrorMessage || entry.APIError != "usage_limit_reached" {
		return "", false
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) == nil && len(blocks) > 0 {
		return blocks[0].Text, true
	}
	var text string
	_ = json.Unmarshal(entry.Message.Content, &text)
	return text, true
}

var resetRe = regexp.MustCompile(`resets\s+(?:([A-Za-z]{3})[a-z]*\.?\s+(\d{1,2}),?\s+(?:at\s+)?)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)(?:\s*\(([^)]+)\))?`)

// fallbackCooldown applies when the reset time can't be parsed: the
// session window is five hours.
const fallbackCooldown = 5 * time.Hour

// ParseReset extracts the reset time from a limit message such as
// "… · resets 3:40pm (Europe/Paris)" or "… · resets Nov 2, 3pm (Europe/Paris)".
func ParseReset(msg string, now time.Time) time.Time {
	m := resetRe.FindStringSubmatch(msg)
	if m == nil {
		return now.Add(fallbackCooldown)
	}
	loc := time.Local
	if m[6] != "" {
		if l, err := time.LoadLocation(m[6]); err == nil {
			loc = l
		}
	}
	hour, _ := strconv.Atoi(m[3])
	minute, _ := strconv.Atoi(m[4])
	hour %= 12
	if m[5] == "pm" {
		hour += 12
	}
	local := now.In(loc)
	y, mon, d := local.Date()
	if m[1] != "" {
		if t, err := time.Parse("Jan", strings.ToUpper(m[1][:1])+strings.ToLower(m[1][1:])); err == nil {
			mon = t.Month()
			d, _ = strconv.Atoi(m[2])
		}
	}
	t := time.Date(y, mon, d, hour, minute, 0, 0, loc)
	if !t.After(now) {
		if m[1] != "" {
			t = t.AddDate(1, 0, 0)
		} else {
			t = t.AddDate(0, 0, 1)
		}
	}
	return t
}
