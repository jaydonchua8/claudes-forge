# Ops console

`forge_ops.py` turns the hook log at `~/.notchforge/events.jsonl` into a session report. It prints a plain-text summary and can also write a self-contained HTML report.

It uses only the Python 3 standard library.

## What it reports

- **Sessions**: grouped by `session_id`, with working directory, turn count, total tool calls, notification count, and the most-used tools.
- **Turns**: a turn starts at `UserPromptSubmit` and ends at `Stop`. Each turn shows its status (finished or unfinished), prompt preview (first 80 characters, whitespace collapsed), tool calls with a per-tool breakdown, and notification count (permission prompts).
- **Unfinished turns**: a turn still open at the end of the log, or superseded by a new prompt before its `Stop`, is marked unfinished.
- **Tool calls** are counted from `PreToolUse` events, so each call is counted once.
- **Duration** is shown only when both the `UserPromptSubmit` and `Stop` events carry a numeric `ts` field (epoch seconds). Otherwise it shows `n/a`.
- **Malformed lines** (not JSON, not an object, or no string `hook_event_name`) are skipped and counted. They carry no session, so the count covers the whole log rather than each session.

## How to run

```sh
python3 ops/forge_ops.py                       # text summary of ~/.notchforge/events.jsonl
python3 ops/forge_ops.py --events PATH         # a different events file
python3 ops/forge_ops.py --html report.html    # also write the HTML report
python3 -m unittest discover -s ops -v         # run the tests
```

## Sample data

To try the report without real Claude Code activity, write a sample log to a temp file and point the tool at it:

```sh
cat > /tmp/forge-sample.jsonl <<'EOF'
{"hook_event_name":"UserPromptSubmit","session_id":"test-1","cwd":"/Users/me/leetstrat","prompt":"fix the failing\nauth test","ts":1767225600}
{"hook_event_name":"PreToolUse","session_id":"test-1","cwd":"/Users/me/leetstrat","tool_name":"Bash","tool_input":{"command":"pytest tests/test_auth.py -x"},"ts":1767225601}
{"hook_event_name":"Notification","session_id":"test-1","cwd":"/Users/me/leetstrat","message":"Claude needs your permission to use Bash","ts":1767225603}
{"hook_event_name":"PostToolUse","session_id":"test-1","cwd":"/Users/me/leetstrat","tool_name":"Bash","tool_input":{"command":"pytest"},"ts":1767225605}
{"hook_event_name":"PreToolUse","session_id":"test-1","cwd":"/Users/me/leetstrat","tool_name":"mcp__github__create_pr","tool_input":{},"ts":1767225606}
{"hook_event_name":"Stop","session_id":"test-1","cwd":"/Users/me/leetstrat","ts":1767225612}
{"hook_event_name":"UserPromptSubmit","session_id":"test-2","cwd":"/Users/me/site","prompt":"refactor the nav"}
{"hook_event_name":"PreToolUse","session_id":"test-2","cwd":"/Users/me/site","tool_name":"Read","tool_input":{"file_path":"nav.tsx"}}
EOF
python3 ops/forge_ops.py --events /tmp/forge-sample.jsonl --html /tmp/forge-report.html
```

The first session has `ts` fields, so its turn shows a duration of 12.0s. The second has none, so its turn shows `n/a`. The second turn is unfinished because the log ends before its `Stop`.

## Durations need the `ts` field

The hook in `install.sh` adds `ts` (epoch seconds) to every event. Events recorded before that change have no `ts`, so their durations show `n/a`.

**Existing installs must re-run `./install.sh`** to start recording timestamps. Re-running is safe.
