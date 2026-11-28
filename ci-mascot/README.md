# ci-mascot

`ci_mascot.py` reads a JSON list of recent GitHub Actions runs and writes a small
state file that a desktop mascot can read. The pixel-robot in Claude's Forge sleeps
when CI is green and wakes with an alarm when CI breaks.

This project is only the state tool. The mascot UI itself is not part of this project.

## States

The tool looks at the most recent run of each workflow (grouped by the run's `name`,
newest `created_at` first) and decides the state in this order:

| state     | meaning |
|-----------|---------|
| `alarm`   | The latest completed run of some workflow ended with `failure`, `timed_out` or `startup_failure`. `failing` lists those workflow names. |
| `working` | Nothing has failed, and at least one run is `queued` or `in_progress`. In-progress runs never count as failures. |
| `sleep`   | Every latest completed run is green (`success`, `skipped`, `neutral` or `cancelled`). Also used when there are no runs at all (`reason` is `no runs`). |

Output file fields:

- `state`: `alarm`, `working` or `sleep`
- `reason`: short human-readable explanation
- `failing`: sorted list of failing workflow names (empty unless `alarm`)
- `updated_from`: number of runs considered (after the branch filter)
- `source_newest`: `created_at` of the newest run considered, or `null`

## Commands

Run the tool on a runs file:

```
python3 ci_mascot.py --runs RUNS.json --out STATE.json
python3 ci_mascot.py --runs RUNS.json --out STATE.json --branch-filter main
```

The state is printed to stdout and written to `--out` as indented JSON. The exit code
is 0 for every state, so this is a status tool by default.

`--gate` makes it usable as a CI check: it exits 1 when the state is `alarm`
(the state file is still written first).

```
python3 ci_mascot.py --runs RUNS.json --out STATE.json --gate
```

Exit code 2 means the runs file could not be read or the state file could not be written.

Run the tests:

```
python3 -m unittest -v test_ci_mascot
```

## Producing RUNS.json with gh

Requires the `gh` CLI, authenticated for the repository:

```
gh api --paginate repos/OWNER/REPO/actions/runs > runs.json
```

or a single page of 50 runs:

```
gh api 'repos/OWNER/REPO/actions/runs?per_page=50' > runs.json
```

`--paginate` prints one JSON document per page back to back, and the tool reads all of
them. To get a single JSON document instead, add `--slurp` on recent `gh` versions.

To read the state from a file the mascot can watch:

```
python3 ci_mascot.py --runs runs.json --out /path/to/state.json --branch-filter main
```

## How a mascot uses the state file

The mascot only needs the `state` field of the output file: `sleep` shows the robot
asleep, `working` shows it busy, and `alarm` wakes it with the alarm. Re-run the tool
(and re-fetch the runs) on whatever schedule the mascot should refresh at. The other
fields (`reason`, `failing`, `source_newest`) are there for tooltips or logs.

## Fields used from each run

`name`, `status`, `conclusion`, `created_at`, plus `head_branch` for the optional
`--branch-filter`. `html_url` and other fields are ignored.

## Driving Claude's Forge

Claude's Forge polls `~/.notchforge/ci-state.json` every five seconds. Write the state there:

```sh
python3 ci_mascot.py --runs runs.json --out ~/.notchforge/ci-state.json
```

- `alarm` shows an amber "CI failing" alert with the failing workflow names. Click the island to dismiss it. It stays dismissed until CI changes.
- `working` starts the anvil animation with "CI running…".
- `sleep` clears the CI alert or the CI animation. Claude's own alerts and work are never cleared by CI.

Run it on a schedule (for example, from cron or a launch agent) to keep the robot current.
