# Claude's Forge

A little pixel robot blacksmith that lives at the top of your screen (in the MacBook notch, or top center on Windows) and shows what Claude Code is doing.

- **Idle:** he sleeps in a midnight-blue bed in a cozy cave.
- **Working:** he walks to his anvil and hammers away. Each hit shows a line of what Claude is doing (your prompt, shell commands, code being written, files being read), with a timer.
- **Done:** a chime, a green banner, and he walks back to bed.
- **Needs you:** an amber alert with a ping when Claude is waiting on a permission prompt.

It works by tailing `~/.notchforge/events.jsonl`, which Claude Code [hooks](https://docs.claude.com/en/docs/claude-code/hooks) append to.

## macOS

### Requirements

- macOS 14+ (built for MacBooks with a notch; falls back to the menu bar on other screens)
- Xcode command line tools (`swiftc`)
- `jq` (ships with macOS 15+ at `/usr/bin/jq`)
- Claude Code

### Install

```sh
./build.sh     # compiles NotchForge.app into ~/Applications
./install.sh   # adds the Claude Code hooks and a login item
```

`install.sh` backs up `~/.claude/settings.json` to `settings.json.notchforge-backup` and keeps your existing settings.

### Use

- Run any `claude` session and watch the notch.
- Left-click the island to dismiss a Done or Needs-you alert.
- Right-click for Run demo, Mute sounds, Reset and Quit.
- `./test.sh` sends fake hook events for a quick check.

### Uninstall

```sh
./uninstall.sh
```

Removes the hooks, the login item, the app and `~/.notchforge`.

## Windows

The Windows version lives in [`windows/`](windows). It is a native Win32 app written in Go with no runtime to install.

### Install

1. Download `claudes-forge-windows.zip` from the [latest release](../../releases/latest) and unzip it.
2. Run `claudes-forge.exe`. Windows SmartScreen may warn because the app is unsigned; choose **More info**, then **Run anyway**.

That copies the app to `%LOCALAPPDATA%\ClaudesForge`, adds the Claude Code hooks to `%USERPROFILE%\.claude\settings.json` (the original is backed up as `settings.json.claudes-forge-backup`), starts it at sign-in, and launches it at the top center of your main screen.

Left-click dismisses an alert. Right-click opens Run demo, Mute sounds, Reset and Quit.

### Uninstall

Run `claudes-forge.exe --uninstall` from the unzipped folder.

### Build from source

```sh
cd windows
./build.sh        # needs Go 1.22+, works from macOS, Linux or Windows (Git Bash)
go run .          # on macOS/Linux: renders preview PNGs of the demo
go test ./...
```
