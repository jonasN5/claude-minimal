package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func fdIdentity(t *testing.T, fd int) (uint64, int32) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Ino), int32(st.Dev)
}

// Both our own fd-2 writes and those of child processes (the macOS malloc
// warning comes from a fork child) must land in the log, and restore must
// give fd 2 back.
func TestRedirectStderr(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "stderr.log")
	ino, dev := fdIdentity(t, unix.Stderr)

	restore := redirectStderr(path)
	_, _ = unix.Write(unix.Stderr, []byte("direct write\n"))
	child := exec.Command("/bin/sh", "-c", "echo from child >&2")
	child.Stderr = os.Stderr
	runErr := child.Run()
	restore()

	if runErr != nil {
		t.Fatal(runErr)
	}
	if gotIno, gotDev := fdIdentity(t, unix.Stderr); gotIno != ino || gotDev != dev {
		t.Fatal("fd 2 was not restored")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"claude-minimal pid", "direct write", "from child"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("log lacks %q:\n%s", want, data)
		}
	}
}

func TestRedirectStderrTruncatesLargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxStderrLog+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	redirectStderr(path)()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 200 {
		t.Fatalf("log not truncated: %d bytes", fi.Size())
	}
}
