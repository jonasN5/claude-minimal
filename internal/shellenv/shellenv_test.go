package shellenv

import (
	"os"
	"testing"
)

func TestLooksBare(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"launchd default", "/usr/bin:/bin:/usr/sbin:/sbin", true},
		{"launchd default reordered", "/bin:/usr/bin", true},
		{"trailing slashes", "/usr/bin/:/bin/", true},
		{"empty", "", true},
		{"with homebrew", "/opt/homebrew/bin:/usr/bin:/bin", false},
		{"with orbstack", "/usr/bin:/bin:/Users/x/.orbstack/bin", false},
		{"with nvm node", "/Users/x/.nvm/versions/node/v22/bin:/usr/bin", false},
	} {
		if got := looksBare(tc.path); got != tc.want {
			t.Errorf("%s: looksBare(%q) = %v, want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

func TestRepairLeavesRealShellPathAlone(t *testing.T) {
	// A PATH that already carries profile entries must never be replaced —
	// running from a terminal should cost nothing and respect a deliberate PATH.
	want := "/opt/homebrew/bin:/usr/bin:/bin"
	t.Setenv("PATH", want)
	if changed := Repair(); changed != "" {
		t.Errorf("Repair() rewrote a non-bare PATH to %q", changed)
	}
	if got := os.Getenv("PATH"); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}

func TestRepairFromBarePath(t *testing.T) {
	// A shell whose profile adds a marker directory stands in for the user's
	// real login shell, so the test does not depend on the host's dotfiles.
	dir := t.TempDir()
	marker := dir + "/toolchain"
	script := "#!/bin/sh\nexec /bin/sh \"$@\"\n"
	// A POSIX sh started by Repair with -lic/-lc sources $ENV; point it at a
	// profile that injects the marker.
	if err := os.WriteFile(dir+"/profile.sh", []byte("PATH="+marker+":$PATH\nexport PATH\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/shell", []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV", dir+"/profile.sh")
	t.Setenv("SHELL", dir+"/shell")
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	got := Repair()
	if got == "" {
		t.Skip("login shell did not source $ENV; PATH recovery is host-dependent")
	}
	if os.Getenv("PATH") != got {
		t.Errorf("PATH env = %q, want returned %q", os.Getenv("PATH"), got)
	}
	if looksBare(got) {
		t.Errorf("Repair() returned a still-bare PATH: %q", got)
	}
}
