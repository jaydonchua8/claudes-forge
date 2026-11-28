#!/usr/bin/env python3
"""Session and turn report for Claude's Forge hook events.

Reads the JSON lines that the NotchForge hook appends to
~/.notchforge/events.jsonl and prints a plain-text summary. With --html it
also writes a self-contained HTML report.

Usage:
    python3 ops/forge_ops.py [--events PATH] [--html OUT.html]

Standard library only.
"""

from __future__ import annotations

import argparse
import html
import json
import os
import sys
from collections import Counter
from dataclasses import dataclass, field
from typing import Iterable

DEFAULT_EVENTS = "~/.notchforge/events.jsonl"
PROMPT_PREVIEW_CHARS = 80
TOP_TOOLS = 5
UNKNOWN_TOOL = "(unknown)"
NO_SESSION = "(no session_id)"
NO_PROMPT = "(no prompt)"


@dataclass
class Turn:
    session_id: str
    number: int
    prompt: str
    finished: bool = False
    tool_calls: int = 0
    tool_counts: Counter = field(default_factory=Counter)
    notifications: int = 0
    duration: float | None = None


@dataclass
class Session:
    session_id: str
    cwd: str | None = None
    turns: list[Turn] = field(default_factory=list)
    tool_calls: int = 0
    tool_counts: Counter = field(default_factory=Counter)
    notifications: int = 0


@dataclass
class Report:
    sessions: list[Session]
    valid_events: int
    malformed: int


def parse_lines(lines: Iterable[str]) -> tuple[list[dict], int]:
    """Return (valid events, malformed line count). Blank lines are ignored.

    A line is malformed if it is not JSON, is not a JSON object, or has no
    string hook_event_name.
    """
    events: list[dict] = []
    malformed = 0
    for raw in lines:
        line = raw.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            malformed += 1
            continue
        if not isinstance(obj, dict) or not isinstance(obj.get("hook_event_name"), str):
            malformed += 1
            continue
        events.append(obj)
    return events, malformed


def _ts(event: dict) -> float | None:
    """Epoch seconds from the event's ts field, or None if absent or not a number."""
    value = event.get("ts")
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    return float(value)


def _prompt_preview(prompt: object) -> str:
    text = " ".join(prompt.split()) if isinstance(prompt, str) else ""
    if not text:
        return NO_PROMPT
    return text[:PROMPT_PREVIEW_CHARS]


def _tool_name(event: dict) -> str:
    name = event.get("tool_name")
    return name if isinstance(name, str) and name else UNKNOWN_TOOL


def analyze(events: Iterable[dict]) -> dict[str, Session]:
    """Group events by session and split each session into turns.

    A turn starts at UserPromptSubmit and ends at Stop. A turn still open when
    the log ends, or superseded by a new prompt, stays unfinished. Tool calls
    are counted from PreToolUse events, so each call is counted once.
    """
    sessions: dict[str, Session] = {}
    open_turns: dict[str, tuple[Turn, float | None]] = {}

    for ev in events:
        raw_sid = ev.get("session_id")
        sid = str(raw_sid) if raw_sid not in (None, "") else NO_SESSION
        session = sessions.get(sid)
        if session is None:
            session = sessions[sid] = Session(session_id=sid)
        if session.cwd is None and isinstance(ev.get("cwd"), str):
            session.cwd = ev["cwd"]

        kind = ev["hook_event_name"]
        if kind == "UserPromptSubmit":
            open_turns.pop(sid, None)
            turn = Turn(
                session_id=sid,
                number=len(session.turns) + 1,
                prompt=_prompt_preview(ev.get("prompt")),
            )
            session.turns.append(turn)
            open_turns[sid] = (turn, _ts(ev))
        elif kind == "PreToolUse":
            name = _tool_name(ev)
            session.tool_calls += 1
            session.tool_counts[name] += 1
            if sid in open_turns:
                turn = open_turns[sid][0]
                turn.tool_calls += 1
                turn.tool_counts[name] += 1
        elif kind == "Notification":
            session.notifications += 1
            if sid in open_turns:
                open_turns[sid][0].notifications += 1
        elif kind == "Stop" and sid in open_turns:
            turn, start = open_turns.pop(sid)
            turn.finished = True
            end = _ts(ev)
            if start is not None and end is not None:
                turn.duration = end - start

    return sessions


def build_report(events: list[dict], malformed: int) -> Report:
    sessions = list(analyze(events).values())
    return Report(sessions=sessions, valid_events=len(events), malformed=malformed)


def format_duration(seconds: float | None) -> str:
    return "n/a" if seconds is None else f"{seconds:.1f}s"


def _status(turn: Turn) -> str:
    return "finished" if turn.finished else "unfinished"


def _top_tools(counts: Counter, limit: int | None = None) -> list[tuple[str, int]]:
    ordered = sorted(counts.items(), key=lambda kv: (-kv[1], kv[0]))
    return ordered if limit is None else ordered[:limit]


def _tools_text(counts: Counter, limit: int | None = None) -> str:
    items = _top_tools(counts, limit)
    if not items:
        return "none"
    return ", ".join(f"{name} x{count}" for name, count in items)


def format_text(report: Report) -> str:
    total_turns = sum(len(s.turns) for s in report.sessions)
    total_tools = sum(s.tool_calls for s in report.sessions)
    total_notifs = sum(s.notifications for s in report.sessions)

    lines = [
        "Claude's Forge ops report",
        f"Events: {report.valid_events} valid, "
        f"{report.malformed} malformed (skipped; not attributed to sessions)",
        f"Sessions: {len(report.sessions)}  Turns: {total_turns}  "
        f"Tool calls: {total_tools}  Notifications: {total_notifs}",
    ]
    if not report.sessions:
        lines.append("")
        lines.append("No sessions found.")
        return "\n".join(lines) + "\n"

    for session in report.sessions:
        lines.append("")
        lines.append(f"Session {session.session_id}  cwd: {session.cwd or '(no cwd)'}")
        lines.append(
            f"  Turns: {len(session.turns)}  Tool calls: {session.tool_calls}  "
            f"Notifications: {session.notifications}"
        )
        lines.append(f"  Most-used tools: {_tools_text(session.tool_counts, TOP_TOOLS)}")
        for turn in session.turns:
            lines.append(
                f"  Turn {turn.number:>2}  {_status(turn):<10}  "
                f"tools {turn.tool_calls:>3}  notifications {turn.notifications:>2}  "
                f"duration {format_duration(turn.duration):>7}  {turn.prompt}"
            )
            lines.append(f"           tools: {_tools_text(turn.tool_counts)}")
    return "\n".join(lines) + "\n"


_HTML_STYLE = """
body { font-family: -apple-system, system-ui, sans-serif; margin: 24px; color: #1d1d1f; background: #fafafa; }
h1 { font-size: 22px; margin: 0 0 4px; }
h2 { font-size: 16px; margin: 28px 0 8px; }
.muted { color: #6e6e73; font-size: 13px; }
table { border-collapse: collapse; width: 100%; background: #fff; font-size: 13px; }
th, td { border-bottom: 1px solid #e5e5ea; padding: 6px 8px; text-align: left; vertical-align: top; }
th { background: #f0f0f3; font-weight: 600; }
td.num { text-align: right; font-variant-numeric: tabular-nums; }
.unfinished { color: #b25000; }
.bar-row { display: flex; align-items: center; gap: 8px; margin: 4px 0; font-size: 13px; }
.bar-label { width: 260px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.track { flex: 1; background: #e5e5ea; border-radius: 3px; height: 14px; }
.bar { background: #3a6ea5; height: 14px; border-radius: 3px; }
.bar-count { width: 48px; text-align: right; font-variant-numeric: tabular-nums; }
"""


def format_html(report: Report, events_label: str) -> str:
    esc = html.escape
    total_turns = sum(len(s.turns) for s in report.sessions)
    total_tools = sum(s.tool_calls for s in report.sessions)
    total_notifs = sum(s.notifications for s in report.sessions)

    session_rows = []
    turn_rows = []
    overall: Counter = Counter()
    for session in report.sessions:
        overall.update(session.tool_counts)
        session_rows.append(
            "<tr>"
            f"<td>{esc(session.session_id)}</td>"
            f"<td>{esc(session.cwd or '(no cwd)')}</td>"
            f"<td class='num'>{len(session.turns)}</td>"
            f"<td class='num'>{session.tool_calls}</td>"
            f"<td class='num'>{session.notifications}</td>"
            "</tr>"
        )
        for turn in session.turns:
            status_class = "" if turn.finished else " class='unfinished'"
            turn_rows.append(
                "<tr>"
                f"<td>{esc(session.session_id)}</td>"
                f"<td class='num'>{turn.number}</td>"
                f"<td{status_class}>{_status(turn)}</td>"
                f"<td>{esc(turn.prompt)}</td>"
                f"<td class='num'>{turn.tool_calls}</td>"
                f"<td class='num'>{turn.notifications}</td>"
                f"<td class='num'>{esc(format_duration(turn.duration))}</td>"
                f"<td>{esc(_tools_text(turn.tool_counts))}</td>"
                "</tr>"
            )

    tool_items = _top_tools(overall)
    max_count = max((count for _, count in tool_items), default=0)
    bars = []
    for name, count in tool_items:
        width = count / max_count * 100 if max_count else 0.0
        bars.append(
            "<div class='bar-row'>"
            f"<span class='bar-label' title='{esc(name)}'>{esc(name)}</span>"
            f"<div class='track'><div class='bar' style='width:{width:.1f}%'></div></div>"
            f"<span class='bar-count'>{count}</span>"
            "</div>"
        )
    bar_block = "\n".join(bars) if bars else "<p class='muted'>No tool calls recorded.</p>"

    session_table = (
        "<table><thead><tr><th>Session</th><th>Working directory</th><th>Turns</th>"
        "<th>Tool calls</th><th>Notifications</th></tr></thead><tbody>"
        + "".join(session_rows)
        + "</tbody></table>"
        if session_rows
        else "<p class='muted'>No sessions found.</p>"
    )
    turn_table = (
        "<table><thead><tr><th>Session</th><th>Turn</th><th>Status</th><th>Prompt</th>"
        "<th>Tool calls</th><th>Notifications</th><th>Duration</th><th>Tools</th>"
        "</tr></thead><tbody>" + "".join(turn_rows) + "</tbody></table>"
        if turn_rows
        else "<p class='muted'>No turns recorded.</p>"
    )

    return (
        "<!DOCTYPE html>\n"
        "<html lang='en'>\n<head>\n<meta charset='utf-8'>\n"
        "<meta name='viewport' content='width=device-width, initial-scale=1'>\n"
        "<title>Claude's Forge ops report</title>\n"
        f"<style>{_HTML_STYLE}</style>\n</head>\n<body>\n"
        "<h1>Claude's Forge ops report</h1>\n"
        f"<p class='muted'>Source: {esc(events_label)}<br>"
        f"Events: {report.valid_events} valid, {report.malformed} malformed (skipped)<br>"
        f"Sessions: {len(report.sessions)} &middot; Turns: {total_turns} &middot; "
        f"Tool calls: {total_tools} &middot; Notifications: {total_notifs}</p>\n"
        "<h2>Sessions</h2>\n" + session_table + "\n"
        "<h2>Tool calls by tool</h2>\n" + bar_block + "\n"
        "<h2>Turns</h2>\n" + turn_table + "\n"
        "</body>\n</html>\n"
    )


def load_events(path: str) -> tuple[list[dict], int]:
    with open(path, encoding="utf-8", errors="replace") as fh:
        return parse_lines(fh)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Summarize Claude's Forge hook events by session and turn."
    )
    parser.add_argument(
        "--events",
        default=DEFAULT_EVENTS,
        help=f"path to events JSON lines (default: {DEFAULT_EVENTS})",
    )
    parser.add_argument(
        "--html", metavar="OUT.html", help="also write a self-contained HTML report"
    )
    args = parser.parse_args(argv)

    events_path = os.path.expanduser(args.events)
    try:
        events, malformed = load_events(events_path)
    except OSError as exc:
        print(f"forge_ops: cannot read {events_path}: {exc}", file=sys.stderr)
        return 1

    report = build_report(events, malformed)
    sys.stdout.write(format_text(report))

    if args.html:
        try:
            with open(args.html, "w", encoding="utf-8") as fh:
                fh.write(format_html(report, events_path))
        except OSError as exc:
            print(f"forge_ops: cannot write {args.html}: {exc}", file=sys.stderr)
            return 1
        print(f"HTML report written to {args.html}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
