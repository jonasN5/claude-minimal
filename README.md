# claude-minimal

A minimal, fast terminal session manager for [Claude Code](https://claude.com/claude-code). A single small Go binary (~5 MB), no tmux dependency, near-zero idle resource usage.

Built as a lighter alternative to claude-squad, with a different workflow: sessions are defined by the **project context** they load, provisioning is **automated by hooks**, and there is **no pause/resume ceremony** — sessions can always be killed and picked up again instantly.

## Features

### Project-context sessions
The app launches over a top-level projects folder (default `~/projects`). Creating a session (⌥n) asks for a name, then which project(s) to load:

- **one repo** — classic single-project session;
- **several repos together** — e.g. a backend and its sibling service; all of them are checked out into one combined workspace so Claude sees the full cross-repo context;
- **none** — a free-form session with a scratch workspace.

Each selected repo becomes a **git worktree** on a dedicated `session/<name>` branch (or a symlink with `worktree = false`), and a generated workspace `CLAUDE.md` tells Claude what's checked out where.

### Provisioning and kill hooks
Each project can declare, in the config:

- a **`setup` hook** — runs inside the fresh worktree before Claude starts, streaming its output into the session pane. Use it to clone a dev database, assign per-session ports, start docker services, etc.
- a **`teardown` (kill) hook** — runs when the session is deleted (⌥d). Teardown runs **in the background**: the session shows `…` in the list until cleanup finishes and the rest of the UI stays usable. Output goes to the session's `teardown.log`.

Hooks receive `CLAUDE_MINIMAL_SESSION=<name>` in their environment and run with stdin from `/dev/null` — use non-interactive flags (`--force`, `--yes`); there is no TTY to answer prompts on.

### Instant resume, no pause/resume
The tail of every conversation is ANSI-stripped and auto-saved to a `context.md` file in the session directory (every 5 seconds and on exit). Quit whenever you like: selecting a stopped session and pressing a key relaunches it with `claude --continue`, which restores Claude's own conversation history — `context.md` covers anything else and is referenced from the workspace `CLAUDE.md`.

### Live, typeable conversation pane
The dashboard lists sessions on the left and shows the **live** conversation of the selected session on the right — a real in-process terminal emulator attached to Claude's PTY, not a preview snapshot. You type straight into it; there is no "press enter to attach" step. Switching sessions (⌥↑/↓ or a click) swaps the pane instantly.

### Pane-aware mouse
- **Drag inside the conversation pane** to select text. The selection is scoped to the pane — it never bleeds into the session list — and is **copied to the clipboard automatically on release** (the status bar confirms with "✓ copied N chars").
- **Click a session** in the list to switch to it.
- **Scroll wheel** is forwarded as arrow keys, matching Terminal.app's alternate-screen behavior.
- **⌘V paste** is forwarded to Claude as a bracketed paste (multi-line pastes stay one block).

### Pre-trusted workspaces
Session workspaces are created fresh, so Claude Code would normally show its "do you trust this folder?" dialog every single time. claude-minimal pre-registers each workspace it creates as an accepted folder in `~/.claude.json` before launch — only for its own workspaces, via an atomic parse-safe merge that never rewrites a config it couldn't parse.

### Safe deletion
Deleting a session refuses (without `f`) if any worktree has **uncommitted changes** or **commits that exist on no remote** (measured against the branch's recorded base commit, so it works in repos without remotes too). Force-delete is always available and still runs teardown hooks.

### Unseen-output indicator
When Claude finishes a turn in a session you are **not** currently viewing, that session shows an orange `✻` in the list until you switch to it — so after a system notification you know exactly which conversation to check. The marker clears the moment the session is on screen.

The indicator is driven by a [Claude Code Stop hook](https://docs.claude.com/en/docs/claude-code/hooks) that drops a `needs-attention` file in the session directory. Add it to `~/.claude/settings.json` (it ignores sessions not managed by claude-minimal):

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "jq -r '.cwd // empty' | { read -r d; case \"$d\" in \"$HOME\"/.claude-minimal/sessions/*/workspace*) touch \"${d%%/workspace*}/needs-attention\";; esac; } 2>/dev/null || true"
          }
        ]
      }
    ]
  }
}
```

Pair it with a second Stop hook command that runs `osascript -e 'display notification ...'` (or `notify-send` on Linux) if you also want a system notification when Claude finishes.

### Stable session list
Sessions are listed oldest-first with stable numbers — a session keeps its number for its whole lifetime and new sessions append at the bottom. Status dots: `●` running, `○` stopped, `✻` finished with unseen output, `…` deleting. With no sessions at all, a full-screen welcome takes over.

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
claude-minimal              # uses the configured root (default ~/projects)
claude-minimal -root ~/work
```

### Keys

| Key | Action |
|-----|--------|
| ⌥n | New session (name → project multi-select) |
| ⌥↑ / ⌥↓ (or Ctrl+↑/↓) | Switch session — the right pane follows instantly |
| ⌥d | Delete session (`y` confirm · `f` force · `esc` cancel) |
| anything else | Typed straight into the selected session (incl. ⌥⏎ newline) |

There is no quit key — just close the terminal window. Context tails are auto-saved every 5 seconds, so sessions resume where they left off.

In the **new-session wizard**: type a name (empty = timestamp) → `enter` → `space` toggles projects → `enter` creates; `esc` goes back/cancels.

A stopped session shows a placeholder; the first keypress relaunches it with `claude --continue` (that keypress is not forwarded).

### Mouse

| Gesture | Action |
|---------|--------|
| Drag in conversation pane | Select pane text; auto-copies to clipboard on release |
| Click session in list | Switch to that session |
| Scroll wheel | Forwarded as arrow keys |
| ⌘V | Bracketed paste into the session |

## Configuration

`~/.config/claude-minimal/config.toml` — everything is optional; the app works with zero config.

```toml
root = "~/projects"        # folder scanned for repos; also the -root flag
auto_discover = false      # false = picker offers ONLY the [[project]] entries
# scan_depth = 2           # how deep under root to look for git repos
claude_cmd = "claude"      # binary launched in each session
# claude_args = ["--model", "opus"]
# data_dir = "~/.claude-minimal"   # session state, worktrees, context tails

[[project]]
name = "med"
path = "~/projects/personal/med"
# Runs inside the fresh worktree before Claude starts, streaming into the pane.
# e.g. clone the dev DB, assign per-session ports, start docker services:
setup = "./scripts/claude-session-start-hook.sh"
# Kill hook: runs when the session is deleted (⌥d), in the background —
# use --force / non-interactive flags, hooks have no TTY to prompt on:
teardown = "./scripts/cleanup-session.sh --force"

[[project]]
name = "bilan-prevention"
path = "~/projects/personal/bilan-prevention"

[[project]]
name = "notes"
path = "~/projects/personal/notes"
worktree = false   # symlink the repo instead of isolating it
```

| Key | Default | Meaning |
|-----|---------|---------|
| `root` | `~/projects` | Folder the app launches over; scanned for git repos |
| `auto_discover` | `true` | Add repos found under `root` to the project picker; `false` restricts the picker to `[[project]]` entries |
| `scan_depth` | `2` | Directory levels under `root` to scan |
| `claude_cmd` / `claude_args` | `claude` / `[]` | Command launched in each session |
| `data_dir` | `~/.claude-minimal` | Where sessions live |
| `[[project]].worktree` | `true` | `false` symlinks the repo instead of creating a worktree |
| `[[project]].setup` | — | Provisioning hook (first launch, in-pane) |
| `[[project]].teardown` | — | Kill hook (on delete, background, → `teardown.log`) |

## How a session is laid out

```
~/.claude-minimal/sessions/<name>/
├── meta.json        # projects, branches, base commits
├── setup.sh         # generated from the project setup hooks
├── context.md       # auto-saved conversation tail (every 5s + on exit)
├── needs-attention  # marker: Claude finished, output not yet viewed (via Stop hook)
├── teardown.log     # kill-hook output (during deletion)
└── workspace/       # Claude's working directory
    ├── CLAUDE.md    # generated: lists each project, points at context.md
    ├── med/         # git worktree on branch session/<name>
    └── bilan-prevention/
```

Deleting a session (⌥d) kills the process, runs teardown hooks, removes worktrees and their `session/<name>` branches, and deletes the directory — with the unpushed-work guard described above.

## Design notes

- The right pane is a real in-process terminal emulator ([vt10x](https://github.com/hinshun/vt10x)) attached to Claude's PTY — not a tmux attach. That's what makes select-and-type instant and keeps resource usage tiny.
- Because sessions run in-process, closing the app's terminal window stops them; that's by design. Resume is cheap: `claude --continue` restores Claude's own conversation history, and `context.md` covers anything else.
- Idle footprint is the Go binary plus one PTY per running session. No daemons, no tmux servers.
- Built with [bubbletea](https://github.com/charmbracelet/bubbletea), [lipgloss](https://github.com/charmbracelet/lipgloss), [creack/pty](https://github.com/creack/pty) and [vt10x](https://github.com/hinshun/vt10x).

## License

MIT
