#!/bin/sh
# Removes the Claude Code hooks, the login item and the app.
HOOK="$HOME/.notchforge/hook.sh"
SETTINGS="$HOME/.claude/settings.json"
PLIST="$HOME/Library/LaunchAgents/dev.jaydon.notchforge.plist"
launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null
pkill -x NotchForge 2>/dev/null
rm -f "$PLIST"
if [ -f "$SETTINGS" ]; then
  /usr/bin/jq --arg cmd "$HOOK" '
    if .hooks then
      .hooks |= (with_entries(.value |= map(select(((.hooks // []) | any(.command == $cmd)) | not)))
                 | with_entries(select(.value | length > 0)))
      | if (.hooks | length) == 0 then del(.hooks) else . end
    else . end' "$SETTINGS" > "$SETTINGS.tmp" && mv "$SETTINGS.tmp" "$SETTINGS"
fi
rm -rf "$HOME/.notchforge" "$HOME/Applications/NotchForge.app"
echo "uninstalled"
