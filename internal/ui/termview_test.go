package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hinshun/vt10x"
)

// feedLines writes n numbered lines into a fresh 20x5 emulator with history.
func feedLines(t *testing.T, n int) vt10x.Terminal {
	t.Helper()
	vt := vt10x.New(vt10x.WithSize(20, 5), vt10x.WithHistorySize(100))
	for i := 1; i <= n; i++ {
		if _, err := fmt.Fprintf(vt, "line%d\r\n", i); err != nil {
			t.Fatal(err)
		}
	}
	return vt
}

func TestScrollbackCapture(t *testing.T) {
	vt := feedLines(t, 12)
	vt.Lock()
	hlen := vt.HistoryLen()
	vt.Unlock()
	// 12 lines + trailing prompt row on a 5-row screen: 8 scrolled off.
	if hlen != 8 {
		t.Fatalf("HistoryLen = %d, want 8", hlen)
	}
	vt.Lock()
	var b strings.Builder
	for x := 0; x < 5; x++ {
		b.WriteRune(vt.HistoryCell(0, x).Char)
	}
	vt.Unlock()
	if got := strings.TrimRight(b.String(), " \x00"); got != "line1" {
		t.Fatalf("oldest history line = %q, want line1", got)
	}
}

func TestRenderTerminalScrollback(t *testing.T) {
	vt := feedLines(t, 12)

	live := RenderTerminal(vt, 20, 5, false, nil, -1)
	if !strings.Contains(live, "line12") || strings.Contains(live, "line1\x1b") {
		t.Fatalf("live view should show the latest lines, got:\n%q", live)
	}

	top := RenderTerminal(vt, 20, 5, false, nil, 0)
	for _, want := range []string{"line1", "line5"} {
		if !strings.Contains(top, want) {
			t.Fatalf("scrollback view at 0 should contain %q, got:\n%q", want, top)
		}
	}
	if strings.Contains(top, "line12") {
		t.Fatalf("scrollback view at 0 should not show live rows, got:\n%q", top)
	}

	// A viewport straddling history and screen composes both.
	mid := RenderTerminal(vt, 20, 5, false, nil, 6)
	for _, want := range []string{"line7", "line11"} {
		if !strings.Contains(mid, want) {
			t.Fatalf("straddling view should contain %q, got:\n%q", want, mid)
		}
	}

	// Out-of-range positions clamp instead of panicking.
	_ = RenderTerminal(vt, 20, 5, false, nil, 9999)
}

// historyLine reads back one history line as trimmed text.
// Caller must hold the vt lock.
func historyLine(vt vt10x.Terminal, i int) string {
	var b strings.Builder
	for x := 0; x < 20; x++ {
		b.WriteRune(vt.HistoryCell(i, x).Char)
	}
	return strings.TrimRight(b.String(), " \x00")
}

// Ink-style repaints (frame taller than the window) erase screen+scrollback
// with CSI 2J / 3J and rewrite the whole frame each tick. The overflow that
// scrolls off must end up in history once, not once per repaint.
func TestEraseScrollbackRepaintCycle(t *testing.T) {
	vt := feedLines(t, 12) // history now holds line1..line8
	var frame strings.Builder
	frame.WriteString("\x1b[2J\x1b[3J\x1b[H")
	for i := 1; i <= 7; i++ {
		fmt.Fprintf(&frame, "F%d\r\n", i)
	}
	for r := 0; r < 3; r++ {
		fmt.Fprint(vt, frame.String())
	}

	vt.Lock()
	defer vt.Unlock()
	// 8 old lines + the 3 frame lines that overflow a 5-row screen.
	if n := vt.HistoryLen(); n != 11 {
		t.Fatalf("HistoryLen = %d, want 11 (repaints must not accumulate)", n)
	}
	dupes := 0
	for i := 0; i < vt.HistoryLen(); i++ {
		if historyLine(vt, i) == "F1" {
			dupes++
		}
	}
	if dupes != 1 {
		t.Fatalf("frame line F1 appears %d times in history, want 1", dupes)
	}
	if got := historyLine(vt, 0); got != "line1" {
		t.Fatalf("pre-repaint history lost: oldest line = %q, want line1", got)
	}
}

func TestScrollbackAltScreenNotCaptured(t *testing.T) {
	vt := vt10x.New(vt10x.WithSize(20, 5), vt10x.WithHistorySize(100))
	fmt.Fprint(vt, "\x1b[?1049h") // enter alt screen
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(vt, "alt%d\r\n", i)
	}
	fmt.Fprint(vt, "\x1b[?1049l") // leave alt screen
	vt.Lock()
	defer vt.Unlock()
	if n := vt.HistoryLen(); n != 0 {
		t.Fatalf("alt-screen output must not enter history, got %d lines", n)
	}
}
