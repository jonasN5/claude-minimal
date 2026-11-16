package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/jonasN5/claude-minimal/internal/config"
)

// maxStderrLog caps the log: past this size it starts over on the next launch.
const maxStderrLog = 1 << 20

func stderrLogPath() string {
	return filepath.Join(filepath.Dir(config.Path()), "stderr.log")
}

// redirectStderr points file descriptor 2 at the log file at path while the
// TUI owns the terminal. Whatever reaches fd 2 would otherwise be drawn on top
// of the screen: under memory pressure macOS's malloc prints "MallocStackLogging:
// can't turn off malloc stack logging because it was not enabled." from our
// fork children before they exec, and crashes outside Bubble Tea print there
// too. The redirect is done on the descriptor, not just os.Stderr, because
// libc and forked children write to fd 2 directly.
//
// It returns a function that makes the terminal stderr again. If the log
// can't be set up, stderr is left alone and the function does nothing.
func redirectStderr(path string) (restore func()) {
	noop := func() {}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return noop
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxStderrLog {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return noop
	}
	defer f.Close() // fd 2 keeps the file open
	// Close-on-exec so sessions don't inherit a handle to our terminal.
	saved, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return noop
	}
	if err := unix.Dup2(int(f.Fd()), unix.Stderr); err != nil {
		unix.Close(saved)
		return noop
	}
	fmt.Fprintf(os.Stderr, "--- claude-minimal pid %d started %s\n", os.Getpid(), time.Now().Format(time.DateTime))
	return func() {
		_ = unix.Dup2(saved, unix.Stderr)
		unix.Close(saved)
	}
}
