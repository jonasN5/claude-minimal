package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// keyToBytes converts a bubbletea key event back into the byte sequence a
// terminal would send, for forwarding into the session PTY.
func keyToBytes(k tea.KeyMsg) []byte {
	if k.Paste {
		// Forward as a bracketed paste so multi-line pastes stay one block.
		return append(append([]byte("\x1b[200~"), []byte(string(k.Runes))...), []byte("\x1b[201~")...)
	}
	var seq string
	switch k.Type {
	case tea.KeyRunes:
		s := string(k.Runes)
		if k.Alt {
			s = "\x1b" + s
		}
		return []byte(s)
	case tea.KeySpace:
		seq = " "
	case tea.KeyEnter:
		seq = "\r"
		if k.Alt { // alt+enter = newline in Claude Code's input
			seq = "\x1b\r"
		}
	case tea.KeyBackspace:
		seq = "\x7f"
		if k.Alt {
			seq = "\x1b\x7f"
		}
	case tea.KeyTab:
		seq = "\t"
	case tea.KeyShiftTab:
		seq = "\x1b[Z"
	case tea.KeyEsc:
		seq = "\x1b"
	case tea.KeyUp:
		seq = "\x1b[A"
	case tea.KeyDown:
		seq = "\x1b[B"
	case tea.KeyRight:
		seq = "\x1b[C"
	case tea.KeyLeft:
		seq = "\x1b[D"
	case tea.KeyShiftUp:
		seq = "\x1b[1;2A"
	case tea.KeyShiftDown:
		seq = "\x1b[1;2B"
	case tea.KeyShiftRight:
		seq = "\x1b[1;2C"
	case tea.KeyShiftLeft:
		seq = "\x1b[1;2D"
	case tea.KeyHome:
		seq = "\x1b[H"
	case tea.KeyEnd:
		seq = "\x1b[F"
	case tea.KeyPgUp:
		seq = "\x1b[5~"
	case tea.KeyPgDown:
		seq = "\x1b[6~"
	case tea.KeyDelete:
		seq = "\x1b[3~"
	case tea.KeyCtrlA:
		seq = "\x01"
	case tea.KeyCtrlB:
		seq = "\x02"
	case tea.KeyCtrlC:
		seq = "\x03"
	case tea.KeyCtrlD:
		seq = "\x04"
	case tea.KeyCtrlE:
		seq = "\x05"
	case tea.KeyCtrlF:
		seq = "\x06"
	case tea.KeyCtrlG:
		seq = "\x07"
	case tea.KeyCtrlJ:
		seq = "\n"
	case tea.KeyCtrlK:
		seq = "\x0b"
	case tea.KeyCtrlL:
		seq = "\x0c"
	case tea.KeyCtrlN:
		seq = "\x0e"
	case tea.KeyCtrlO:
		seq = "\x0f"
	case tea.KeyCtrlP:
		seq = "\x10"
	case tea.KeyCtrlR:
		seq = "\x12"
	case tea.KeyCtrlS:
		seq = "\x13"
	case tea.KeyCtrlT:
		seq = "\x14"
	case tea.KeyCtrlU:
		seq = "\x15"
	case tea.KeyCtrlV:
		seq = "\x16"
	case tea.KeyCtrlW:
		seq = "\x17"
	case tea.KeyCtrlX:
		seq = "\x18"
	case tea.KeyCtrlY:
		seq = "\x19"
	case tea.KeyCtrlZ:
		seq = "\x1a"
	case tea.KeyF1:
		seq = "\x1bOP"
	case tea.KeyF2:
		seq = "\x1bOQ"
	case tea.KeyF3:
		seq = "\x1bOR"
	case tea.KeyF4:
		seq = "\x1bOS"
	default:
		return nil
	}
	return []byte(seq)
}
