#!/bin/sh
# Sends fake Claude Code hook events through the same filter the real hook uses.
cd "$(dirname "$0")"
mkdir -p "$HOME/.notchforge" shots
send() {
  printf '%s' "$1" | /usr/bin/jq -c 'del(.tool_response) | walk(if type == "string" then .[0:400] else . end)' >> "$HOME/.notchforge/events.jsonl"
}
shot() { [ -n "$SHOTS" ] && screencapture -x -R476,0,560,130 "shots/$1.png"; }
S='"session_id":"test-1","cwd":"/Users/me/leetstrat"'
send "{\"hook_event_name\":\"UserPromptSubmit\",$S,\"prompt\":\"fix the failing\\nauth test\"}"
sleep 1.5; shot 1
send "{\"hook_event_name\":\"PreToolUse\",$S,\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"pytest tests/test_auth.py -x\"}}"
sleep 1.5
send "{\"hook_event_name\":\"Notification\",$S,\"message\":\"Claude needs your permission to use Bash\"}"
sleep 1.2; shot 2
send "{\"hook_event_name\":\"PostToolUse\",$S,\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"pytest\"},\"tool_response\":{\"stdout\":\"big\"}}"
send "{\"hook_event_name\":\"PreToolUse\",$S,\"tool_name\":\"mcp__github__create_pr\",\"tool_input\":{}}"
sleep 1.3; shot 3
send "{\"hook_event_name\":\"Stop\",$S}"
sleep 1.5; shot 4
sleep 10.5; shot 5
