"""Tests for forge_ops. Run with: python3 -m unittest discover -s ops -v"""

import contextlib
import io
import json
import os
import tempfile
import unittest

import forge_ops


def ev(kind, session="s1", **extra):
    event = {"hook_event_name": kind, "session_id": session, "cwd": "/tmp/proj"}
    event.update(extra)
    return event


def write_lines(directory, lines):
    path = os.path.join(directory, "events.jsonl")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write("\n".join(lines) + "\n")
    return path


class ParseTests(unittest.TestCase):
    def test_valid_lines_parsed_blank_and_malformed_handled(self):
        lines = [
            json.dumps(ev("UserPromptSubmit", prompt="hi")),
            "",
            "   ",
            "{not json",
            "[1, 2, 3]",
            json.dumps({"session_id": "s1"}),
            json.dumps({"hook_event_name": 5}),
            json.dumps(ev("Stop")),
        ]
        events, malformed = forge_ops.parse_lines(lines)
        self.assertEqual(len(events), 2)
        self.assertEqual(malformed, 4)
        self.assertEqual(events[0]["prompt"], "hi")
        self.assertEqual(events[1]["hook_event_name"], "Stop")

    def test_load_events_reads_file(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = write_lines(tmp, [json.dumps(ev("Stop")), "garbage"])
            events, malformed = forge_ops.load_events(path)
        self.assertEqual(len(events), 1)
        self.assertEqual(malformed, 1)


class TurnGroupingTests(unittest.TestCase):
    def test_sessions_and_turns_grouped(self):
        events = [
            ev("UserPromptSubmit", session="a", prompt="first"),
            ev("Stop", session="a"),
            ev("UserPromptSubmit", session="b", prompt="other session"),
            ev("UserPromptSubmit", session="a", prompt="second"),
            ev("Stop", session="a"),
        ]
        sessions = forge_ops.analyze(events)
        self.assertEqual(list(sessions), ["a", "b"])
        self.assertEqual([t.prompt for t in sessions["a"].turns], ["first", "second"])
        self.assertEqual([t.number for t in sessions["a"].turns], [1, 2])
        self.assertTrue(all(t.finished for t in sessions["a"].turns))
        self.assertEqual(sessions["b"].cwd, "/tmp/proj")

    def test_open_turn_at_end_of_log_is_unfinished(self):
        events = [ev("UserPromptSubmit", prompt="still going"), ev("PreToolUse", tool_name="Bash")]
        turn = forge_ops.analyze(events)["s1"].turns[0]
        self.assertFalse(turn.finished)
        self.assertEqual(turn.tool_calls, 1)
        self.assertIsNone(turn.duration)

    def test_new_prompt_before_stop_leaves_previous_turn_unfinished(self):
        events = [
            ev("UserPromptSubmit", prompt="one"),
            ev("UserPromptSubmit", prompt="two"),
            ev("Stop"),
        ]
        turns = forge_ops.analyze(events)["s1"].turns
        self.assertFalse(turns[0].finished)
        self.assertTrue(turns[1].finished)

    def test_stop_without_open_turn_is_ignored(self):
        sessions = forge_ops.analyze([ev("Stop")])
        self.assertEqual(sessions["s1"].turns, [])

    def test_missing_session_id_grouped_under_placeholder(self):
        sessions = forge_ops.analyze([{"hook_event_name": "Notification", "message": "x"}])
        self.assertIn(forge_ops.NO_SESSION, sessions)
        self.assertEqual(sessions[forge_ops.NO_SESSION].notifications, 1)


class ToolAndNotificationCountTests(unittest.TestCase):
    def test_tool_counts_per_turn_and_session(self):
        events = [
            ev("UserPromptSubmit", prompt="go"),
            ev("PreToolUse", tool_name="Bash"),
            ev("PostToolUse", tool_name="Bash"),
            ev("PreToolUse", tool_name="Bash"),
            ev("PreToolUse", tool_name="Read"),
            ev("Stop"),
            ev("PreToolUse", tool_name="Edit"),
        ]
        session = forge_ops.analyze(events)["s1"]
        turn = session.turns[0]
        self.assertEqual(turn.tool_calls, 3)
        self.assertEqual(turn.tool_counts, {"Bash": 2, "Read": 1})
        self.assertEqual(session.tool_calls, 4)
        self.assertEqual(session.tool_counts["Edit"], 1)

    def test_missing_tool_name_counted_as_unknown(self):
        events = [ev("UserPromptSubmit", prompt="x"), ev("PreToolUse")]
        turn = forge_ops.analyze(events)["s1"].turns[0]
        self.assertEqual(turn.tool_counts[forge_ops.UNKNOWN_TOOL], 1)

    def test_notifications_counted_inside_turn(self):
        events = [
            ev("Notification", message="outside"),
            ev("UserPromptSubmit", prompt="x"),
            ev("Notification", message="permission"),
            ev("Stop"),
        ]
        session = forge_ops.analyze(events)["s1"]
        self.assertEqual(session.turns[0].notifications, 1)
        self.assertEqual(session.notifications, 2)


class DurationTests(unittest.TestCase):
    def test_duration_when_ts_on_start_and_end(self):
        events = [
            ev("UserPromptSubmit", prompt="x", ts=1000),
            ev("Stop", ts=1012.5),
        ]
        turn = forge_ops.analyze(events)["s1"].turns[0]
        self.assertEqual(turn.duration, 12.5)
        self.assertIn("12.5s", forge_ops.format_text(forge_ops.build_report(events, 0)))

    def test_n_a_when_ts_absent(self):
        events = [ev("UserPromptSubmit", prompt="x"), ev("Stop")]
        turn = forge_ops.analyze(events)["s1"].turns[0]
        self.assertIsNone(turn.duration)
        text = forge_ops.format_text(forge_ops.build_report(events, 0))
        self.assertIn("duration     n/a", text)

    def test_n_a_when_ts_only_on_one_side(self):
        events = [ev("UserPromptSubmit", prompt="x", ts=1000), ev("Stop")]
        self.assertIsNone(forge_ops.analyze(events)["s1"].turns[0].duration)

    def test_non_numeric_ts_ignored(self):
        events = [
            ev("UserPromptSubmit", prompt="x", ts="1000"),
            ev("Stop", ts=True),
        ]
        self.assertIsNone(forge_ops.analyze(events)["s1"].turns[0].duration)


class HtmlTests(unittest.TestCase):
    def test_prompt_is_escaped(self):
        events = [
            ev("UserPromptSubmit", prompt="<script>alert('x')</script>"),
            ev("Stop"),
        ]
        out = forge_ops.format_html(forge_ops.build_report(events, 0), "test")
        self.assertNotIn("<script>alert", out)
        self.assertIn("&lt;script&gt;alert(&#x27;x&#x27;)&lt;/script&gt;", out)

    def test_html_has_table_bars_and_no_external_assets(self):
        events = [
            ev("UserPromptSubmit", prompt="go"),
            ev("PreToolUse", tool_name="Bash"),
            ev("Stop"),
        ]
        out = forge_ops.format_html(forge_ops.build_report(events, 0), "test")
        self.assertIn("<table", out)
        self.assertIn("class='bar'", out)
        self.assertNotIn("<link", out)
        self.assertNotIn("<script", out)
        self.assertNotIn("src=", out)

    def test_tool_name_escaped_in_bar_chart(self):
        events = [ev("UserPromptSubmit", prompt="go"), ev("PreToolUse", tool_name="<b>x</b>")]
        out = forge_ops.format_html(forge_ops.build_report(events, 0), "test")
        self.assertNotIn("<b>x</b>", out)


class PromptPreviewTests(unittest.TestCase):
    def test_prompt_truncated_to_80_chars(self):
        prompt = "a" * 200
        turn = forge_ops.analyze([ev("UserPromptSubmit", prompt=prompt)])["s1"].turns[0]
        self.assertEqual(turn.prompt, "a" * 80)

    def test_newlines_collapsed_for_display(self):
        turn = forge_ops.analyze([ev("UserPromptSubmit", prompt="fix the\nfailing\ttest")])["s1"].turns[0]
        self.assertEqual(turn.prompt, "fix the failing test")


class MainTests(unittest.TestCase):
    def test_cli_writes_text_and_html(self):
        with tempfile.TemporaryDirectory() as tmp:
            events_path = write_lines(
                tmp,
                [
                    json.dumps(ev("UserPromptSubmit", prompt="fix <tests>", ts=10)),
                    json.dumps(ev("PreToolUse", tool_name="Bash")),
                    "not json",
                    json.dumps(ev("Stop", ts=14)),
                ],
            )
            html_path = os.path.join(tmp, "report.html")
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                code = forge_ops.main(["--events", events_path, "--html", html_path])
            self.assertEqual(code, 0)
            text = buf.getvalue()
            self.assertIn("1 malformed", text)
            self.assertIn("4.0s", text)
            self.assertIn("fix <tests>", text)
            with open(html_path, encoding="utf-8") as fh:
                page = fh.read()
            self.assertIn("fix &lt;tests&gt;", page)
            self.assertIn("4.0s", page)

    def test_missing_events_file_returns_error(self):
        with tempfile.TemporaryDirectory() as tmp:
            err = io.StringIO()
            with contextlib.redirect_stderr(err), contextlib.redirect_stdout(io.StringIO()):
                code = forge_ops.main(["--events", os.path.join(tmp, "nope.jsonl")])
            self.assertEqual(code, 1)
            self.assertIn("cannot read", err.getvalue())

    def test_empty_log_reports_no_sessions(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = write_lines(tmp, [])
            buf = io.StringIO()
            with contextlib.redirect_stdout(buf):
                code = forge_ops.main(["--events", path])
            self.assertEqual(code, 0)
            self.assertIn("No sessions found.", buf.getvalue())


if __name__ == "__main__":
    unittest.main()
