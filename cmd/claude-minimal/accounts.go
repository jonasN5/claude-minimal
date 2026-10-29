package main

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// authEmail reports which login an account's credential slot holds.
//
// `claude auth status` can't answer this: it reads the email from the shared
// ~/.claude.json, which every login overwrites, so all slots would show the
// most recent login. Instead, read the slot's own token from the keychain and
// ask Anthropic whose it is.
func authEmail(cfg *config.Config, pool *account.Pool, name string) string {
	out, err := exec.Command("security", "find-generic-password", "-s", pool.KeychainService(name), "-w").Output()
	if err != nil {
		return "(not signed in)"
	}
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(out, &creds) != nil || creds.ClaudeAiOauth.AccessToken == "" {
		return "(no claude.ai login)"
	}
	req, _ := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+creds.ClaudeAiOauth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "(profile lookup failed)"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		// Claude Code refreshes the token on its next run.
		return "(signed in; token expired)"
	}
	var profile struct {
		Account struct {
			Email string `json:"email"`
		} `json:"account"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&profile) != nil || profile.Account.Email == "" {
		return fmt.Sprintf("(profile lookup failed: %s)", resp.Status)
	}
	return profile.Account.Email
}
