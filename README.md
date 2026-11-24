# Claude's Forge

A little pixel robot blacksmith that lives in your MacBook's notch and shows what Claude Code is doing.

- **Idle:** he sleeps in a midnight-blue bed in a cozy cave.
- **Working:** he walks to his anvil and hammers away. Each hit shows a line of what Claude is doing (your prompt, shell commands, code being written, files being read), with a timer.
- **Done:** a chime, a green banner, and he walks back to bed.
- **Needs you:** an amber alert with a ping when Claude is waiting on a permission prompt.

It works by tailing `~/.notchforge/events.jsonl`, which Claude Code [hooks](https://docs.claude.com/en/docs/claude-code/hooks) append to.

## Requirements

- macOS 14+ (built for MacBooks with a notch; falls back to the menu bar on other screens)
- Xcode command line tools (`swiftc`)
- `jq` (ships with macOS 15+ at `/usr/bin/jq`)
- Claude Code

## Install

```sh
./build.sh     # compiles NotchForge.app into ~/Applications
./install.sh   # adds the Claude Code hooks and a login item
```

`install.sh` backs up `~/.claude/settings.json` to `settings.json.notchforge-backup` and keeps your existing settings.

## Use

- Run any `claude` session and watch the notch.
- Left-click the island to dismiss a Done or Needs-you alert.
- Right-click for Run demo, Mute sounds, Reset and Quit.
- `./test.sh` sends fake hook events for a quick check.

## Uninstall

```sh
./uninstall.sh
```

Removes the hooks, the login item, the app and `~/.notchforge`.
