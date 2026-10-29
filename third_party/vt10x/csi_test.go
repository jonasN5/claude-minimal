package vt10x

import (
	"testing"
)

func TestCSIParse(t *testing.T) {
	var csi csiEscape
	csi.reset()
	csi.buf = []byte("s")
	csi.parse()
	if csi.mode != 's' || csi.arg(0, 17) != 17 || len(csi.args) != 0 {
		t.Fatal("CSI parse mismatch")
	}

	csi.reset()
	csi.buf = []byte("31T")
	csi.parse()
	if csi.mode != 'T' || csi.arg(0, 0) != 31 || len(csi.args) != 1 {
		t.Fatal("CSI parse mismatch")
	}

	csi.reset()
	csi.buf = []byte("48;2f")
	csi.parse()
	if csi.mode != 'f' || csi.arg(0, 0) != 48 || csi.arg(1, 0) != 2 || len(csi.args) != 2 {
		t.Fatal("CSI parse mismatch")
	}

	csi.reset()
	csi.buf = []byte("?25l")
	csi.parse()
	if csi.mode != 'l' || csi.arg(0, 0) != 25 || csi.priv != true || len(csi.args) != 1 {
		t.Fatal("CSI parse mismatch")
	}
}

// Claude Code ends its startup by drawing the prompt, then querying the
// terminal (XTVERSION, kitty keyboard, DA1) and redrawing relative to the
// cursor. Those queries must not move the cursor or change attributes.
func TestPrivateCSIExtensionsAreIgnored(t *testing.T) {
	term := New(WithSize(20, 10))
	// Save the cursor at the origin, then park it on the prompt row.
	term.Write([]byte("\x1b7\x1b8\x1b[6;3H"))
	term.Write([]byte("\x1b[>0q\x1b[?u\x1b[<u\x1b[>1u\x1b[>4m\x1b[2 q\x1b[?1s"))
	cur := term.Cursor()
	if cur.X != 2 || cur.Y != 5 {
		t.Fatalf("cursor moved to %d,%d; want 2,5", cur.X, cur.Y)
	}
	term.Write([]byte("x"))
	if mode := term.Cell(2, 5).Mode; mode&attrUnderline != 0 {
		t.Fatal("CSI > 4 m was applied as SGR underline")
	}

	// Plain CSI s / CSI u still save and restore.
	term.Write([]byte("\x1b[s\x1b[1;1H\x1b[u"))
	if cur := term.Cursor(); cur.X != 3 || cur.Y != 5 {
		t.Fatalf("CSI s/u: cursor at %d,%d; want 3,5", cur.X, cur.Y)
	}
}

func TestCSIParseMarkers(t *testing.T) {
	var csi csiEscape
	csi.reset()
	csi.buf = []byte(">4m")
	csi.parse()
	if csi.mode != 'm' || csi.marker != '>' || csi.priv || csi.arg(0, 0) != 4 {
		t.Fatalf("CSI > 4 m parsed as %+v", csi)
	}
	csi.reset()
	csi.buf = []byte("2 q")
	csi.parse()
	if csi.mode != 'q' || !csi.inter || csi.arg(0, 0) != 2 {
		t.Fatalf("CSI 2 SP q parsed as %+v", csi)
	}
}
