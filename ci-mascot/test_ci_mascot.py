import contextlib
import io
import json
import os
import tempfile
import unittest

import ci_mascot

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURES = os.path.join(HERE, "fixtures")


def run(name, status="completed", conclusion="success", created_at="2026-11-27T10:00:00Z",
        branch="main"):
    return {
        "name": name,
        "head_branch": branch,
        "status": status,
        "conclusion": conclusion,
        "created_at": created_at,
    }


def quiet_main(argv):
    """Run ci_mascot.main with stdout captured; return (exit_code, stdout_text)."""
    buf = io.StringIO()
    with contextlib.redirect_stdout(buf), contextlib.redirect_stderr(io.StringIO()):
        code = ci_mascot.main(argv)
    return code, buf.getvalue()


class StateRuleTests(unittest.TestCase):

    def test_all_green_is_sleep(self):
        state = ci_mascot.evaluate([run("CI"), run("Lint")])
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["failing"], [])
        self.assertEqual(state["reason"], "all latest completed runs are green")

    def test_failure_is_alarm(self):
        state = ci_mascot.evaluate([run("CI", conclusion="failure"), run("Lint")])
        self.assertEqual(state["state"], "alarm")
        self.assertEqual(state["failing"], ["CI"])
        self.assertEqual(state["reason"], "1 workflow(s) failing: CI")

    def test_timed_out_and_startup_failure_are_alarm(self):
        for conclusion in ("timed_out", "startup_failure"):
            with self.subTest(conclusion=conclusion):
                state = ci_mascot.evaluate([run("Deploy", conclusion=conclusion)])
                self.assertEqual(state["state"], "alarm")
                self.assertEqual(state["failing"], ["Deploy"])

    def test_in_progress_is_working(self):
        state = ci_mascot.evaluate([run("CI", status="in_progress", conclusion=None)])
        self.assertEqual(state["state"], "working")
        self.assertEqual(state["failing"], [])

    def test_queued_is_working(self):
        state = ci_mascot.evaluate([run("Deploy", status="queued", conclusion=None)])
        self.assertEqual(state["state"], "working")

    def test_alarm_beats_working(self):
        state = ci_mascot.evaluate([
            run("CI", conclusion="failure", created_at="2026-11-27T10:00:00Z"),
            run("Docs", status="in_progress", conclusion=None, created_at="2026-11-27T11:00:00Z"),
        ])
        self.assertEqual(state["state"], "alarm")

    def test_neutral_conclusions_are_green(self):
        for conclusion in ("success", "skipped", "neutral", "cancelled"):
            with self.subTest(conclusion=conclusion):
                state = ci_mascot.evaluate([run("Release", conclusion=conclusion)])
                self.assertEqual(state["state"], "sleep")

    def test_zero_runs_is_sleep_no_runs(self):
        state = ci_mascot.evaluate([])
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["reason"], "no runs")
        self.assertEqual(state["updated_from"], 0)
        self.assertIsNone(state["source_newest"])

    def test_multiple_failing_workflows_listed_sorted(self):
        state = ci_mascot.evaluate([
            run("Zeta", conclusion="failure"),
            run("Alpha", conclusion="timed_out"),
        ])
        self.assertEqual(state["failing"], ["Alpha", "Zeta"])
        self.assertEqual(state["reason"], "2 workflow(s) failing: Alpha, Zeta")


class LatestRunSelectionTests(unittest.TestCase):

    def test_older_failure_superseded_by_newer_success_is_green(self):
        state = ci_mascot.evaluate([
            run("CI", conclusion="failure", created_at="2026-11-26T09:00:00Z"),
            run("CI", conclusion="success", created_at="2026-11-27T09:00:00Z"),
        ])
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["failing"], [])

    def test_newer_failure_after_older_success_is_alarm(self):
        state = ci_mascot.evaluate([
            run("CI", conclusion="success", created_at="2026-11-26T09:00:00Z"),
            run("CI", conclusion="failure", created_at="2026-11-27T09:00:00Z"),
        ])
        self.assertEqual(state["state"], "alarm")

    def test_input_order_does_not_matter(self):
        runs = [
            run("CI", conclusion="success", created_at="2026-11-27T09:00:00Z"),
            run("CI", conclusion="failure", created_at="2026-11-26T09:00:00Z"),
        ]
        self.assertEqual(ci_mascot.evaluate(runs)["state"], "sleep")
        self.assertEqual(ci_mascot.evaluate(list(reversed(runs)))["state"], "sleep")

    def test_in_progress_run_does_not_hide_latest_completed_failure(self):
        # Newest CI run is still going, but the latest completed CI run failed.
        state = ci_mascot.evaluate([
            run("CI", status="in_progress", conclusion=None, created_at="2026-11-27T12:00:00Z"),
            run("CI", conclusion="failure", created_at="2026-11-27T09:00:00Z"),
        ])
        self.assertEqual(state["state"], "alarm")

    def test_in_progress_newer_than_completed_success_is_working(self):
        state = ci_mascot.evaluate([
            run("CI", status="in_progress", conclusion=None, created_at="2026-11-27T12:00:00Z"),
            run("CI", conclusion="success", created_at="2026-11-27T09:00:00Z"),
        ])
        self.assertEqual(state["state"], "working")

    def test_source_newest_and_updated_from(self):
        state = ci_mascot.evaluate([
            run("CI", created_at="2026-11-26T09:00:00Z"),
            run("Lint", created_at="2026-11-27T16:30:00Z"),
            run("Docs", created_at="2026-11-25T09:00:00Z"),
        ])
        self.assertEqual(state["updated_from"], 3)
        self.assertEqual(state["source_newest"], "2026-11-27T16:30:00Z")


class BranchFilterTests(unittest.TestCase):

    def test_branch_filter_ignores_other_branches(self):
        runs = [
            run("CI", conclusion="failure", branch="feature/x"),
            run("CI", conclusion="success", branch="main"),
        ]
        state = ci_mascot.evaluate(runs, branch="main")
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["updated_from"], 1)

    def test_without_filter_other_branch_failure_alarms(self):
        runs = [
            run("CI", conclusion="failure", branch="feature/x"),
            run("CI", conclusion="success", branch="main"),
        ]
        self.assertEqual(ci_mascot.evaluate(runs)["state"], "alarm")

    def test_branch_filter_with_no_matches_is_sleep(self):
        state = ci_mascot.evaluate([run("CI", branch="feature/x")], branch="main")
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["reason"], "no runs on branch main")
        self.assertEqual(state["updated_from"], 0)

    def test_branch_filter_via_cli(self):
        with tempfile.TemporaryDirectory() as tmp:
            runs_path = os.path.join(tmp, "runs.json")
            out_path = os.path.join(tmp, "state.json")
            doc = {"workflow_runs": [
                run("CI", conclusion="failure", branch="feature/x"),
                run("CI", conclusion="success", branch="main"),
            ]}
            with open(runs_path, "w", encoding="utf-8") as fh:
                json.dump(doc, fh)
            code, out = quiet_main(["--runs", runs_path, "--out", out_path,
                                    "--branch-filter", "main"])
            self.assertEqual(code, 0)
            self.assertEqual(out.strip(), "sleep")


class InputParsingTests(unittest.TestCase):

    def test_empty_workflow_runs_list(self):
        self.assertEqual(ci_mascot.parse_runs_text('{"total_count": 0, "workflow_runs": []}'), [])

    def test_empty_file_contents(self):
        self.assertEqual(ci_mascot.parse_runs_text("   \n"), [])

    def test_concatenated_pages_from_gh_paginate_are_merged(self):
        text = (json.dumps({"workflow_runs": [run("CI")]}) + "\n"
                + json.dumps({"workflow_runs": [run("Lint")]}) + "\n")
        runs = ci_mascot.parse_runs_text(text)
        self.assertEqual([r["name"] for r in runs], ["CI", "Lint"])

    def test_slurped_pages_list_is_merged(self):
        text = json.dumps([{"workflow_runs": [run("CI")]},
                           {"workflow_runs": [run("Lint")]}])
        runs = ci_mascot.parse_runs_text(text)
        self.assertEqual([r["name"] for r in runs], ["CI", "Lint"])

    def test_missing_workflow_runs_key_is_rejected(self):
        # e.g. an API error body such as {"message": "Not Found"}
        with self.assertRaises(ValueError):
            ci_mascot.parse_runs_text('{"message": "Not Found"}')

    def test_bad_json_is_rejected(self):
        with self.assertRaises(ValueError):
            ci_mascot.parse_runs_text("{not json")

    def test_unreadable_input_exits_2(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, _ = quiet_main(["--runs", os.path.join(tmp, "missing.json"),
                                  "--out", os.path.join(tmp, "state.json")])
            self.assertEqual(code, 2)

    def test_unnamed_workflow_is_grouped(self):
        runs = [{"status": "completed", "conclusion": "failure",
                 "created_at": "2026-11-27T10:00:00Z"}]
        state = ci_mascot.evaluate(runs)
        self.assertEqual(state["state"], "alarm")
        self.assertEqual(state["failing"], [ci_mascot.UNNAMED_WORKFLOW])


class FixtureTests(unittest.TestCase):

    def load(self, name):
        return ci_mascot.load_runs(os.path.join(FIXTURES, name))

    def test_green_fixture(self):
        state = ci_mascot.evaluate(self.load("runs_green.json"))
        self.assertEqual(state["state"], "sleep")
        self.assertEqual(state["updated_from"], 5)
        self.assertEqual(state["source_newest"], "2026-11-26T18:12:04Z")

    def test_failing_fixture(self):
        state = ci_mascot.evaluate(self.load("runs_failing.json"))
        self.assertEqual(state["state"], "alarm")
        self.assertEqual(state["failing"], ["CI", "Deploy"])
        self.assertEqual(state["reason"], "2 workflow(s) failing: CI, Deploy")
        self.assertEqual(state["updated_from"], 5)

    def test_running_fixture(self):
        state = ci_mascot.evaluate(self.load("runs_running.json"))
        self.assertEqual(state["state"], "working")
        self.assertEqual(state["failing"], [])
        self.assertEqual(state["updated_from"], 4)
        self.assertEqual(state["source_newest"], "2026-11-27T15:00:00Z")


class CliOutputTests(unittest.TestCase):

    def test_state_file_shape_and_indentation(self):
        with tempfile.TemporaryDirectory() as tmp:
            out_path = os.path.join(tmp, "state.json")
            code, out = quiet_main(["--runs", os.path.join(FIXTURES, "runs_failing.json"),
                                    "--out", out_path])
            self.assertEqual(code, 0)
            self.assertEqual(out.strip(), "alarm")
            with open(out_path, encoding="utf-8") as fh:
                text = fh.read()
            data = json.loads(text)
            self.assertEqual(set(data), {"state", "reason", "failing", "updated_from",
                                         "source_newest"})
            self.assertIn("\n  ", text)  # indented, readable

    def test_exit_0_for_every_state(self):
        for fixture in ("runs_green.json", "runs_failing.json", "runs_running.json"):
            with self.subTest(fixture=fixture), tempfile.TemporaryDirectory() as tmp:
                code, _ = quiet_main(["--runs", os.path.join(FIXTURES, fixture),
                                      "--out", os.path.join(tmp, "s.json")])
                self.assertEqual(code, 0)


class GateTests(unittest.TestCase):

    def test_gate_exits_1_on_alarm_and_still_writes_state(self):
        with tempfile.TemporaryDirectory() as tmp:
            out_path = os.path.join(tmp, "state.json")
            code, out = quiet_main(["--runs", os.path.join(FIXTURES, "runs_failing.json"),
                                    "--out", out_path, "--gate"])
            self.assertEqual(code, 1)
            self.assertEqual(out.strip(), "alarm")
            with open(out_path, encoding="utf-8") as fh:
                self.assertEqual(json.load(fh)["state"], "alarm")

    def test_gate_exits_0_when_not_alarm(self):
        with tempfile.TemporaryDirectory() as tmp:
            for fixture in ("runs_green.json", "runs_running.json"):
                with self.subTest(fixture=fixture):
                    code, _ = quiet_main(["--runs", os.path.join(FIXTURES, fixture),
                                          "--out", os.path.join(tmp, "s.json"), "--gate"])
                    self.assertEqual(code, 0)

    def test_without_gate_alarm_exits_0(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, _ = quiet_main(["--runs", os.path.join(FIXTURES, "runs_failing.json"),
                                  "--out", os.path.join(tmp, "s.json")])
            self.assertEqual(code, 0)

    def test_gate_with_empty_runs_exits_0(self):
        with tempfile.TemporaryDirectory() as tmp:
            runs_path = os.path.join(tmp, "runs.json")
            with open(runs_path, "w", encoding="utf-8") as fh:
                json.dump({"workflow_runs": []}, fh)
            code, out = quiet_main(["--runs", runs_path, "--out",
                                    os.path.join(tmp, "s.json"), "--gate"])
            self.assertEqual(code, 0)
            self.assertEqual(out.strip(), "sleep")


if __name__ == "__main__":
    unittest.main()
