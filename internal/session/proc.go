package session

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// Proc is a live session process: Claude running in a PTY, mirrored into an
// in-process terminal emulator so the UI can render it. The rendered screen
// is periodically snapshotted to the session's context file so a stopped
// session can show a readable preview and resume with recent context.
type Proc struct {
	VT vt10x.Terminal

	cmd    *exec.Cmd
	ptmx   *os.File
	onData func()

	mu         sync.Mutex
	cols, rows int
	exited     bool

	tailPath string
	done     chan struct{}
}

// Start launches argv in a PTY of the given size, in dir.
// onData is called (from a goroutine) whenever the screen may have changed.
func Start(dir string, argv []string, cols, rows int, tailPath string, onData func()) (*Proc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	p := &Proc{
		VT:       vt10x.New(vt10x.WithSize(cols, rows)),
		cmd:      cmd,
		ptmx:     ptmx,
		onData:   onData,
		cols:     cols,
		rows:     rows,
		tailPath: tailPath,
		done:     make(chan struct{}),
	}
	go p.readLoop()
	go p.tailLoop()
	return p, nil
}

func (p *Proc) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			_, _ = p.VT.Write(buf[:n])
			p.onData()
		}
		if err != nil {
			break
		}
	}
	_ = p.cmd.Wait()
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()
	close(p.done)
	p.SaveTail()
	p.onData()
}

func (p *Proc) tailLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			p.SaveTail()
		case <-p.done:
			return
		}
	}
}

// Exited reports whether the process has terminated.
func (p *Proc) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited
}

// Write forwards input bytes to the PTY.
func (p *Proc) Write(b []byte) {
	_, _ = p.ptmx.Write(b)
}

// Resize adjusts both the PTY and the emulator.
func (p *Proc) Resize(cols, rows int) {
	if cols < 2 || rows < 2 {
		return
	}
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	p.VT.Resize(cols, rows)
	p.mu.Lock()
	p.cols, p.rows = cols, rows
	p.mu.Unlock()
}

// Kill terminates the process, saving the context snapshot first (the live
// screen is still intact at this point).
func (p *Proc) Kill() {
	p.SaveTail()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(2 * time.Second):
			_ = p.cmd.Process.Kill()
		}
	}
	_ = p.ptmx.Close()
}

// screenLines renders the emulator's current screen as plain text lines,
// with trailing blank lines and per-line trailing spaces removed.
func (p *Proc) screenLines() []string {
	p.mu.Lock()
	cols, rows := p.cols, p.rows
	p.mu.Unlock()
	p.VT.Lock()
	defer p.VT.Unlock()
	lines := make([]string, 0, rows)
	for y := 0; y < rows; y++ {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			ch := p.VT.Cell(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			b.WriteRune(ch)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// SaveTail snapshots the rendered screen to the context file. Unlike an
// ANSI-stripped raw stream (which glues cursor-positioned text together),
// the rendered screen reads exactly like what was on display.
func (p *Proc) SaveTail() {
	if p.tailPath == "" {
		return
	}
	lines := p.screenLines()
	nonBlank := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonBlank++
		}
	}
	// A nearly-empty screen (e.g. cleared during shutdown) must not clobber
	// a useful earlier snapshot.
	if nonBlank < 2 {
		if _, err := os.Stat(p.tailPath); err == nil {
			return
		}
	}
	out := "# Last screen (auto-saved by claude-minimal)\n\nSaved " +
		time.Now().Format(time.RFC3339) + "\n\n```\n" + strings.Join(lines, "\n") + "\n```\n"
	_ = os.WriteFile(p.tailPath, []byte(out), 0o644)
}
