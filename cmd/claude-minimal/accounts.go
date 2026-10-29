package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/jonasN5/claude-minimal/internal/account"
	"github.com/jonasN5/claude-minimal/internal/config"
)

const usage = `usage:
  claude-minimal                  open the dashboard
  claude-minimal accounts         show each configured account's login and limit state
  claude-minimal login <account>  sign in to an account's credential slot`

// runCommand handles the non-interactive subcommands and returns the exit code.
func runCommand(cfg *config.Config, args []string) int {
	pool := account.NewPool(cfg.DataDir, cfg.Accounts)
	switch {
	case args[0] == "accounts" && len(args) == 1:
		if pool == nil {
			fmt.Fprintln(os.Stderr, "no accounts configured: add e.g. accounts = [\"default\", \"b\", \"c\"] to "+config.Path())
			return 1
		}
		now := time.Now()
		cur, _ := pool.Current(now)
		for _, name := range pool.Names() {
			marker := " "
			if name == cur {
				marker = "*"
			}
			state := "available"
			if until, ok := pool.ExhaustedUntil(name, now); ok {
				state = "limited until " + until.Local().Format("Mon 15:04")
			}
			fmt.Printf("%s %-12s %-32s %s\n", marker, name, authEmail(cfg, pool, name), state)
		}
		return 0

	case args[0] == "login" && len(args) == 2:
		name := args[1]
		if pool == nil || !slices.Contains(pool.Names(), name) {
			fmt.Fprintf(os.Stderr, "unknown account %q: list it under accounts in %s\n", name, config.Path())
			return 1
		}
		cmd := exec.Command(cfg.ClaudeCmd, "auth", "login")
		cmd.Env = append(os.Environ(), pool.Env(name)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "login failed:", err)
			return 1
		}
		fmt.Printf("account %s: signed in as %s\n", name, authEmail(cfg, pool, name))
		return 0
	}
	fmt.Fprintln(os.Stderr, usage)
	return 2
}

// authEmail asks Claude Code which login an account's credential slot holds.
func authEmail(cfg *config.Config, pool *account.Pool, name string) string {
	cmd := exec.Command(cfg.ClaudeCmd, "auth", "status")
	cmd.Env = append(os.Environ(), pool.Env(name)...)
	out, _ := cmd.Output() // exits non-zero when logged out
	var st struct {
		LoggedIn bool   `json:"loggedIn"`
		Email    string `json:"email"`
	}
	if json.Unmarshal(out, &st) != nil {
		return "(status unavailable)"
	}
	if !st.LoggedIn {
		return "(not signed in)"
	}
	return st.Email
}
