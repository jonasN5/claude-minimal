package ui

import (
	"fmt"
	"strings"

	"github.com/hinshun/vt10x"
)

// Attribute bits mirrored from vt10x (unexported there, stable since 2019).
const (
	attrReverse = 1 << iota
	attrUnderline
	attrBold
	attrGfx
	attrItalic
	attrBlink
)

func colorSGR(c vt10x.Color, fg bool) string {
	base := 0
	if !fg {
		base = 10
	}
	switch {
	case c >= 1<<24: // DefaultFG / DefaultBG / DefaultCursor
		return fmt.Sprintf("%d", 39+base)
	case c < 8:
		return fmt.Sprintf("%d", 30+base+int(c))
	case c < 16:
		return fmt.Sprintf("%d", 90+base+int(c-8))
	case c < 256:
		return fmt.Sprintf("%d;5;%d", 38+base, int(c))
	default:
		return fmt.Sprintf("%d;2;%d;%d;%d", 38+base, int(c>>16)&0xff, int(c>>8)&0xff, int(c)&0xff)
	}
}

// SelRange is a normalized linear selection over pane cells:
// (X1,Y1) <= (X2,Y2) in row-major order.
type SelRange struct{ X1, Y1, X2, Y2 int }

func (r *SelRange) contains(x, y int) bool {
	if r == nil {
		return false
	}
	return (y > r.Y1 || (y == r.Y1 && x >= r.X1)) && (y < r.Y2 || (y == r.Y2 && x <= r.X2))
}

// RenderTerminal draws the emulator screen as ANSI-styled lines.
// The cursor cell is drawn reversed when focused; sel cells are highlighted.
func RenderTerminal(vt vt10x.Terminal, cols, rows int, focused bool, sel *SelRange) string {
	vt.Lock()
	defer vt.Unlock()

	cursor := vt.Cursor()
	cursorVisible := focused && vt.CursorVisible()

	var b strings.Builder
	b.Grow(cols * rows * 4)
	for y := 0; y < rows; y++ {
		lastSGR := ""
		b.WriteString("\x1b[0m")
		for x := 0; x < cols; x++ {
			cell := vt.Cell(x, y)
			// Note: vt10x already stores reverse-video cells with FG/BG
			// swapped, so attrReverse needs no handling here.
			fg, bg, mode := cell.FG, cell.BG, cell.Mode
			parts := []string{"0", colorSGR(fg, true), colorSGR(bg, false)}
			if mode&attrBold != 0 {
				parts = append(parts, "1")
			}
			if mode&attrItalic != 0 {
				parts = append(parts, "3")
			}
			if mode&attrUnderline != 0 {
				parts = append(parts, "4")
			}
			if (cursorVisible && x == cursor.X && y == cursor.Y) || sel.contains(x, y) {
				parts = append(parts, "7") // reverse video: cursor / selection
			}
			sgr := strings.Join(parts, ";")
			if sgr != lastSGR {
				b.WriteString("\x1b[" + sgr + "m")
				lastSGR = sgr
			}
			ch := cell.Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		b.WriteString("\x1b[0m")
		if y < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
