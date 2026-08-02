package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureTrusted marks the workspace as an accepted folder in Claude Code's
// config (~/.claude.json), so sessions don't show the "do you trust this
// folder" dialog for every freshly created workspace.
func EnsureTrusted(workspace string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return ensureTrustedIn(filepath.Join(home, ".claude.json"), workspace)
}

func ensureTrustedIn(configPath, workspace string) error {
	root := map[string]any{}
	if data, err := os.ReadFile(configPath); err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber() // preserve number formatting on rewrite
		if err := dec.Decode(&root); err != nil {
			// Never rewrite a config we couldn't parse.
			return fmt.Errorf("parse %s: %w", configPath, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	projects, ok := root["projects"].(map[string]any)
	if !ok {
		projects = map[string]any{}
		root["projects"] = projects
	}
	entry, ok := projects[workspace].(map[string]any)
	if !ok {
		entry = map[string]any{}
		projects[workspace] = entry
	}
	if trusted, _ := entry["hasTrustDialogAccepted"].(bool); trusted {
		return nil // nothing to do — avoids racing running Claude instances
	}
	entry["hasTrustDialogAccepted"] = true
	out, err := json.Marshal(root)
	if err != nil {
		return err
	}
	tmp := configPath + ".claude-minimal.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}
