#!/bin/bash
# Builds "Claude Minimal.app": a native app bundle that hosts claude-minimal
# in an embedded copy of the Alacritty terminal. Because the terminal process
# lives inside this bundle, macOS attributes the window to Claude Minimal
# itself — own Dock icon, active-app highlight, and native single-instance
# behavior (clicking the icon focuses the running window).
#
# Requires: alacritty (brew install --cask alacritty).
# Installs over an existing copy in /Applications or ~/Applications,
# defaulting to ~/Applications for fresh installs.
set -euo pipefail

if [ -d "/Applications/Claude Minimal.app" ]; then
  APP_DIR="/Applications"
else
  APP_DIR="$HOME/Applications"
fi
APP="$APP_DIR/Claude Minimal.app"

BIN="$(command -v claude-minimal || true)"
if [ -z "$BIN" ]; then
  echo "claude-minimal not found in PATH — install it first (go install github.com/jonasN5/claude-minimal/cmd/claude-minimal@latest)" >&2
  exit 1
fi
ALACRITTY="/Applications/Alacritty.app/Contents/MacOS/alacritty"
if [ ! -x "$ALACRITTY" ]; then
  ALACRITTY="$(command -v alacritty || true)"
fi
if [ -z "$ALACRITTY" ]; then
  echo "alacritty not found — install it first (brew install --cask alacritty)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$APP_DIR"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$ALACRITTY" "$APP/Contents/MacOS/alacritty"

# exec keeps the final process inside this bundle, which is what makes macOS
# credit the window to Claude Minimal instead of a terminal app.
cat > "$APP/Contents/MacOS/claude-minimal-host" <<EOF
#!/bin/sh
exec "\$(dirname "\$0")/alacritty" \\
  --title "Claude Minimal" \\
  -o 'window.option_as_alt="Both"' \\
  -o window.dimensions.columns=170 -o window.dimensions.lines=45 \\
  -e "$BIN"
EOF
chmod +x "$APP/Contents/MacOS/claude-minimal-host"

if [ -f "$SCRIPT_DIR/AppIcon.icns" ]; then
  cp "$SCRIPT_DIR/AppIcon.icns" "$APP/Contents/Resources/AppIcon.icns"
fi

cat > "$APP/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>
	<string>Claude Minimal</string>
	<key>CFBundleDisplayName</key>
	<string>Claude Minimal</string>
	<key>CFBundleIdentifier</key>
	<string>com.jonasN5.claude-minimal</string>
	<key>CFBundleExecutable</key>
	<string>claude-minimal-host</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>1.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
EOF

xattr -cr "$APP"
codesign --force --deep -s - "$APP"
echo "Created $APP — launch it from Spotlight or the Dock as 'Claude Minimal'."
