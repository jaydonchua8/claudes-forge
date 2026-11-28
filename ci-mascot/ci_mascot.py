#!/usr/bin/env python3
"""Summarize recent GitHub Actions runs into a small JSON state file.

States:
  alarm    the latest completed run of some workflow ended in failure,
           timed_out or startup_failure
  working  nothing has failed and some run is queued or in progress
  sleep    everything is green, or there are no runs at all

Usage:
  python3 ci_mascot.py --runs RUNS.json --out STATE.json [--branch-filter BRANCH] [--gate]

The runs file is the JSON returned by the GitHub API runs endpoint, with a
top-level "workflow_runs" list. Several concatenated JSON documents (what
`gh api --paginate` prints) and a list of pages (`gh api --paginate --slurp`)
are also accepted.
"""

import argparse
import datetime as dt
import json
import sys

FAILING_CONCLUSIONS = frozenset({"failure", "timed_out", "startup_failure"})
ACTIVE_STATUSES = frozenset({"in_progress", "queued"})
COMPLETED_STATUS = "completed"
UNNAMED_WORKFLOW = "(unnamed workflow)"
_EPOCH = dt.datetime.min.replace(tzinfo=dt.timezone.utc)


def parse_timestamp(value):
    """Return an aware datetime for an ISO 8601 string, or None if unusable."""
    if not isinstance(value, str) or not value:
        return None
    try:
        parsed = dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed


def _recency_key(run):
    return parse_timestamp(run.get("created_at")) or _EPOCH


def _workflow_name(run):
    return run.get("name") or UNNAMED_WORKFLOW


def _runs_from_document(obj):
    """Extract run dicts from one parsed document.

    Accepts an object with a "workflow_runs" list, a list of such page objects
    (what `gh api --paginate --slurp` prints), or a bare list of runs.
    """
    if isinstance(obj, dict):
        if "workflow_runs" not in obj:
            raise ValueError('expected an object with a "workflow_runs" list')
        obj = [obj]
    elif not isinstance(obj, list):
        raise ValueError("expected a JSON object or list")

    runs = []
    for item in obj:
        if not isinstance(item, dict):
            continue
        if "workflow_runs" in item:
            page = item["workflow_runs"]
            if not isinstance(page, list):
                raise ValueError('"workflow_runs" must be a list')
            runs.extend(run for run in page if isinstance(run, dict))
        else:
            runs.append(item)
    return runs


def parse_runs_text(text):
    """Parse one or more back-to-back JSON documents into a flat list of runs."""
    decoder = json.JSONDecoder()
    runs = []
    index = 0
    length = len(text)
    while True:
        while index < length and text[index].isspace():
            index += 1
        if index >= length:
            break
        obj, index = decoder.raw_decode(text, index)
        runs.extend(_runs_from_document(obj))
    return runs


def load_runs(path):
    with open(path, encoding="utf-8") as handle:
        return parse_runs_text(handle.read())


def evaluate(runs, branch=None):
    """Compute the mascot state for a list of run dicts.

    Only the latest completed run of each workflow name decides the alarm.
    Runs that are queued or in progress never count as failures.
    """
    considered = [run for run in runs if branch is None or run.get("head_branch") == branch]
    ordered = sorted(considered, key=_recency_key, reverse=True)

    latest_conclusion = {}
    active = []
    for run in ordered:
        name = _workflow_name(run)
        status = run.get("status")
        if status in ACTIVE_STATUSES:
            active.append(name)
        elif status == COMPLETED_STATUS and name not in latest_conclusion:
            latest_conclusion[name] = run.get("conclusion")

    failing = sorted(name for name, conclusion in latest_conclusion.items()
                     if conclusion in FAILING_CONCLUSIONS)

    if not considered:
        if branch is None:
            state, reason = "sleep", "no runs"
        else:
            state, reason = "sleep", f"no runs on branch {branch}"
    elif failing:
        state = "alarm"
        reason = f"{len(failing)} workflow(s) failing: {', '.join(failing)}"
    elif active:
        state = "working"
        reason = f"{len(active)} run(s) queued or in progress"
    else:
        state = "sleep"
        reason = "all latest completed runs are green"

    return {
        "state": state,
        "reason": reason,
        "failing": failing,
        "updated_from": len(considered),
        "source_newest": ordered[0].get("created_at") if ordered else None,
    }


def write_state(path, state):
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(state, handle, indent=2)
        handle.write("\n")


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Turn GitHub Actions runs into a CI mood state file.")
    parser.add_argument("--runs", required=True,
                        help="JSON file with the runs (gh api repos/OWNER/REPO/actions/runs output)")
    parser.add_argument("--out", required=True, help="where to write the state JSON")
    parser.add_argument("--branch-filter", metavar="BRANCH", default=None,
                        help="only consider runs whose head_branch equals BRANCH")
    parser.add_argument("--gate", action="store_true",
                        help="exit 1 when the state is alarm (for use as a CI check)")
    args = parser.parse_args(argv)

    try:
        runs = load_runs(args.runs)
    except (OSError, ValueError) as exc:
        print(f"ci_mascot: cannot read {args.runs}: {exc}", file=sys.stderr)
        return 2

    state = evaluate(runs, args.branch_filter)
    try:
        write_state(args.out, state)
    except OSError as exc:
        print(f"ci_mascot: cannot write {args.out}: {exc}", file=sys.stderr)
        return 2

    print(state["state"])
    if args.gate and state["state"] == "alarm":
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
