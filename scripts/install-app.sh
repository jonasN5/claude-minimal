#!/bin/bash
# Creates "~/Applications/Claude Minimal.app" — a launcher that opens the
# default terminal app running claude-minimal on your projects root.
set -euo pipefail

APP_DIR="$HOME/Applications"
APP="$APP_DIR/Claude Minimal.app"
BIN="$(command -v claude-minimal || true)"
if [ -z "$BIN" ]; then
  echo "claude-minimal not found in PATH — install it first (go install github.com/jonasN5/claude-minimal/cmd/claude-minimal@latest)" >&2
  exit 1
fi

mkdir -p "$APP_DIR"
TMP_SCRIPT="$(mktemp -t claude-minimal-app).applescript"
cat > "$TMP_SCRIPT" <<EOF
tell application "Terminal"
	activate
	do script "exec '$BIN'"
end tell
EOF

rm -rf "$APP"
osacompile -o "$APP" "$TMP_SCRIPT"
rm -f "$TMP_SCRIPT"
echo "Created $APP — launch it from Spotlight as 'Claude Minimal'."
echo "Tip: enable 'Use Option as Meta key' in Terminal.app profile settings for the ⌥ shortcuts."
