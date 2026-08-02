package session

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

const ringSize = 1 << 20 // 1 MiB of raw output kept for the context tail

// Proc is a live session process: Claude running in a PTY, mirrored into an
// in-process terminal emulator so the UI can render it, plus a ring buffer of
// raw output persisted periodically as the resume context file.
type Proc struct {
	VT vt10x.Terminal

	cmd    *exec.Cmd
	ptmx   *os.File
	onData func()

	mu     sync.Mutex
	ring   []byte
	exited bool

	tailPath  string
	tailLines int
	done      chan struct{}
}

// Start launches argv in a PTY of the given size, in dir.
// onData is called (from a goroutine) whenever the screen may have changed.
func Start(dir string, argv []string, cols, rows int, tailPath string, tailLines int, onData func()) (*Proc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	p := &Proc{
		VT:        vt10x.New(vt10x.WithSize(cols, rows)),
		cmd:       cmd,
		ptmx:      ptmx,
		onData:    onData,
		tailPath:  tailPath,
		tailLines: tailLines,
		done:      make(chan struct{}),
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
			p.mu.Lock()
			p.ring = append(p.ring, buf[:n]...)
			if len(p.ring) > ringSize {
				p.ring = p.ring[len(p.ring)-ringSize:]
			}
			p.mu.Unlock()
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
}

// Kill terminates the process, saving the context tail first.
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

var (
	ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][0-9A-B]|\x1b[=>]|\x1b[78MDE]|[\x00-\x08\x0b\x0c\x0e-\x1f]`)
	crlfRe = regexp.MustCompile(`\r+\n`)
	crRe   = regexp.MustCompile(`[^\n]*\r`)
)

// SaveTail writes the cleaned tail of the conversation to the context file so
// a killed session can be resumed with its recent context intact.
func (p *Proc) SaveTail() {
	if p.tailPath == "" {
		return
	}
	p.mu.Lock()
	raw := string(p.ring)
	p.mu.Unlock()
	text := ansiRe.ReplaceAllString(raw, "")
	text = crlfRe.ReplaceAllString(text, "\n") // PTY ONLCR emits \r\n (or \r\r\n)
	text = crRe.ReplaceAllString(text, "")     // keep only the final content of \r-rewritten lines
	lines := strings.Split(text, "\n")
	// Drop trailing-whitespace noise and collapse blank runs.
	cleaned := make([]string, 0, len(lines))
	blanks := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " ")
		if l == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		cleaned = append(cleaned, l)
	}
	if len(cleaned) > p.tailLines {
		cleaned = cleaned[len(cleaned)-p.tailLines:]
	}
	out := "# Conversation tail (auto-saved by claude-minimal)\n\nSaved " +
		time.Now().Format(time.RFC3339) + "\n\n```\n" + strings.Join(cleaned, "\n") + "\n```\n"
	_ = os.WriteFile(p.tailPath, []byte(out), 0o644)
}
