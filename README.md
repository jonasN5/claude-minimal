# claude-minimal

A minimal, fast terminal session manager for [Claude Code](https://claude.com/claude-code). A single small Go binary, no tmux dependency, near-zero idle resource usage.

Built as a lighter alternative to claude-squad, with a different workflow:

- **Project-context sessions.** The app launches over a top-level projects folder (default `~/projects`). Creating a session asks which project(s) to load — one repo, several repos together, or none for free-form work. All selected repos are checked out (git worktrees) into one combined workspace with a generated `CLAUDE.md`, so Claude sees the full cross-repo context.
- **Provisioning hooks.** Each project can declare a `setup` hook (e.g. a script that clones the dev DB and assigns isolated ports) that runs automatically inside the session pane when the session starts, and a `teardown` hook that runs on deletion.
- **No pause/resume ceremony.** The tail of every conversation is continuously saved to a `context.md` file in the session directory. Quit whenever you like; selecting a stopped session and pressing a key relaunches it with `claude --continue` and the saved context file as backup.
- **Live, typeable preview.** The dashboard lists sessions on the left and shows the **live** conversation of the selected session on the right. You type straight into it — no "press enter to attach" step. Copy/paste works natively (mouse selection is not captured; pastes are forwarded as bracketed paste).

## Install

```bash
go install github.com/jonasN5/claude-minimal/cmd/claude-minimal@latest
```

Or from a clone: `go build -o ~/bin/claude-minimal ./cmd/claude-minimal`

### macOS app shortcut

To get a launchable app (Spotlight/Dock) that opens a terminal directly on your projects root:

```bash
./scripts/install-app.sh          # creates ~/Applications/Claude Minimal.app
```

> **Option key note:** the shortcuts use Alt (⌥). In Terminal.app enable *Use Option as Meta key* (Profiles → Keyboard); in iTerm2 set Option to *Esc+*. Ctrl+↑/↓ also works for switching sessions if you prefer.

## Usage

```bash
claude-minimal              # scans the configured root (default ~/projects)
claude-minimal -root ~/work
```

| Key | Action |
|-----|--------|
| ⌥n | New session (name → project multi-select) |
| ⌥↑ / ⌥↓ (or Ctrl+↑/↓) | Switch session — the right pane follows instantly |
| ⌥d | Delete session (refuses if a worktree holds unpushed work; `f` forces) |
| ⌥l | Toggle focus to the session list (j/k navigation) |
| ⌥q (or Ctrl+q) | Quit — all context tails are saved; sessions resume later |
| anything else | Typed straight into the selected session |

A stopped session shows a placeholder; the first keypress relaunches it with `claude --continue`.

## Configuration

`~/.config/claude-minimal/config.toml` — everything is optional. Repos under `root` are auto-discovered (2 levels deep); `[[project]]` entries add hooks or override defaults.

```toml
root = "~/projects"
# auto_discover = false          # picker offers ONLY the [[project]] entries below
claude_cmd = "claude"
# claude_args = ["--model", "opus"]
# data_dir = "~/.claude-minimal"   # session state, worktrees, context tails
# tail_lines = 2000                # lines of conversation kept in context.md

[[project]]
name = "med"
path = "~/projects/personal/med"
# Runs inside the fresh worktree before Claude starts, streaming into the pane.
# e.g. clone the dev DB, assign per-session ports, start docker services:
setup = "./scripts/claude-session-start-hook.sh"
# Runs on session deletion:
teardown = "./scripts/cleanup-session.sh"

[[project]]
name = "bilan-prevention"
path = "~/projects/personal/bilan-prevention"

[[project]]
name = "notes"
path = "~/projects/personal/notes"
worktree = false   # symlink the repo instead of isolating it
```

Hooks receive `CLAUDE_MINIMAL_SESSION=<name>` in their environment.

## How a session is laid out

```
~/.claude-minimal/sessions/<name>/
├── meta.json        # projects, branches, base commits
├── setup.sh         # generated from the project setup hooks
├── context.md       # auto-saved conversation tail (every 5s + on exit)
└── workspace/       # Claude's working directory
    ├── CLAUDE.md    # generated: lists each project, points at context.md
    ├── med/         # git worktree on branch session/<name>
    └── bilan-prevention/
```

Deleting a session kills the process, runs teardown hooks, removes worktrees and deletes the directory — but refuses (without `f`) if any worktree has uncommitted changes or commits that exist on no remote.

## Design notes

- The right pane is a real in-process terminal emulator ([vt10x](https://github.com/hinshun/vt10x)) attached to Claude's PTY — not a tmux attach. That's what makes select-and-type instant.
- Because sessions run in-process, quitting the app stops them; that's by design. Resume is cheap: `claude --continue` restores Claude's own conversation history, and `context.md` covers anything else.
- Idle footprint is the Go binary plus one PTY per running session.

## License

MIT
