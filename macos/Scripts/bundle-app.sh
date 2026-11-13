#!/usr/bin/env bash
# Build OmaSealBar and wrap it in an .app with LSUIElement set so it does
# not appear in the Dock.
set -euo pipefail

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

CONFIG="${1:-release}"
swift build -c "$CONFIG" --product OmaSealBar

BIN="$(swift build -c "$CONFIG" --product OmaSealBar --show-bin-path)/OmaSealBar"
if [ ! -x "$BIN" ]; then
  echo "bundle-app: built binary not found at $BIN" >&2
  exit 1
fi

APP="${OMASEAL_APP_DIR:-$ROOT/dist}/OmaSealBar.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS"
cp "$BIN" "$APP/Contents/MacOS/OmaSealBar"
chmod +x "$APP/Contents/MacOS/OmaSealBar"

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>OmaSealBar</string>
	<key>CFBundleIdentifier</key>
	<string>com.duketopceo.OmaSealBar</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>OmaSeal</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSMinimumSystemVersion</key>
	<string>14.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
</dict>
</plist>
PLIST

echo "built $APP"
