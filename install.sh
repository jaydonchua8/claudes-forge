#!/bin/sh
# Wires NotchForge into Claude Code (hooks) and starts it at login.
# Safe to run more than once. Undo with ./uninstall.sh
set -e
DIR="$HOME/.notchforge"
HOOK="$DIR/hook.sh"
SETTINGS="$HOME/.claude/settings.json"
PLIST="$HOME/Library/LaunchAgents/dev.jaydon.notchforge.plist"
mkdir -p "$DIR" "$HOME/.claude" "$HOME/Library/LaunchAgents"

# 1. hook script: appends one trimmed JSON line per Claude Code event
cat > "$HOOK" <<'EOF'
#!/bin/sh
/usr/bin/jq -c --argjson ts "$(date +%s)" '. + {ts: $ts} | del(.tool_response) | walk(if type == "string" then .[0:400] else . end)' \
  >> "$HOME/.notchforge/events.jsonl" 2>/dev/null
exit 0
EOF
chmod +x "$HOOK"

# 2. Claude Code hooks (existing settings are kept, backup written first)
[ -f "$SETTINGS" ] || echo '{}' > "$SETTINGS"
cp "$SETTINGS" "$SETTINGS.notchforge-backup"
/usr/bin/jq --arg cmd "$HOOK" '
  def add(ev; m):
    .hooks[ev] = ((.hooks[ev] // [])
      | if any(.[]; (.hooks // []) | any(.command == $cmd)) then .
        else . + [ (if m then {matcher: "*"} else {} end) + {hooks: [{type: "command", command: $cmd}]} ] end);
  .hooks = (.hooks // {})
  | add("UserPromptSubmit"; false) | add("PreToolUse"; true) | add("PostToolUse"; true)
  | add("Stop"; false) | add("Notification"; false) | add("SessionEnd"; false)
' "$SETTINGS.notchforge-backup" > "$SETTINGS.tmp"
mv "$SETTINGS.tmp" "$SETTINGS"

# 3. start at login, restart if it crashes
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.jaydon.notchforge</string>
  <key>ProgramArguments</key>
  <array><string>$HOME/Applications/NotchForge.app/Contents/MacOS/NotchForge</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict>
</plist>
EOF
pkill -x NotchForge 2>/dev/null || true
launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo "installed"
