#!/bin/sh
# Builds NotchForge.app and installs it to ~/Applications.
set -e
cd "$(dirname "$0")"
APP=build/NotchForge.app
rm -rf build
mkdir -p "$APP/Contents/MacOS"
swiftc -O -swift-version 5 -o "$APP/Contents/MacOS/NotchForge" Sources/main.swift
cat > "$APP/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>dev.jaydon.notchforge</string>
  <key>CFBundleName</key><string>NotchForge</string>
  <key>CFBundleExecutable</key><string>NotchForge</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
EOF
codesign --force -s - "$APP" >/dev/null 2>&1
mkdir -p "$HOME/Applications"
pkill -x NotchForge 2>/dev/null || true
rm -rf "$HOME/Applications/NotchForge.app"
cp -R "$APP" "$HOME/Applications/NotchForge.app"
echo "built $HOME/Applications/NotchForge.app"
