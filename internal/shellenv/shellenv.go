// Package shellenv repairs the process environment when claude-minimal is
// started from a GUI launcher rather than a terminal.
//
// A macOS .app bundle inherits launchd's environment, whose PATH is just
// /usr/bin:/bin:/usr/sbin:/sbin — no Homebrew, no nvm, no pyenv, no OrbStack.
// That PATH is passed down to every session process, so Claude and the setup
// hooks it runs cannot find docker, npm, or poetry, and provisioning fails in
// confusing ways. Asking the login shell what PATH a real terminal would have
// restores the developer's toolchain.
package shellenv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// systemPathDirs are the entries launchd hands a GUI process. A PATH built
// only from these means nothing in the user's profile was ever applied.
var systemPathDirs = map[string]bool{
	"/usr/bin":  true,
	"/bin":      true,
	"/usr/sbin": true,
	"/sbin":     true,
}

// looksBare reports whether PATH contains nothing but the system defaults.
func looksBare(path string) bool {
	for _, dir := range filepath.SplitList(path) {
		if dir != "" && !systemPathDirs[strings.TrimSuffix(dir, "/")] {
			return false
		}
	}
	return true
}

// loginShell returns the user's shell, defaulting to sh.
func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// queryPath runs the shell with the given flags and reads back its PATH.
// Profiles are chatty and some print to stderr or expect a tty, so stderr is
// discarded and the call is bounded by a timeout rather than trusted to exit.
func queryPath(shell, flags string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, flags, `printf %s "$PATH"`)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	// An interactive profile may emit banners before our printf; PATH is the
	// last line, since printf writes no trailing newline.
	text := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	return strings.TrimSpace(text)
}

// Repair replaces a bare inherited PATH with the login shell's PATH so child
// processes see the same toolchain an interactive terminal would. It is a
// no-op when the environment already looks like it came from a shell, so
// running from a terminal costs nothing and never overrides a deliberate PATH.
// The repaired value is reported for logging; an empty string means unchanged.
func Repair() string {
	if !looksBare(os.Getenv("PATH")) {
		return ""
	}
	shell := loginShell()
	// -lic sources both the login profile (Homebrew, nvm, OrbStack) and the
	// interactive rc file (pyenv, ~/.local/bin), which is where the rest of
	// the toolchain usually lands. -lc is the fallback for shells that refuse
	// to run interactively without a tty.
	for _, flags := range []string{"-lic", "-lc"} {
		path := queryPath(shell, flags)
		if path == "" || looksBare(path) {
			continue
		}
		if err := os.Setenv("PATH", path); err != nil {
			return ""
		}
		return path
	}
	return ""
}
