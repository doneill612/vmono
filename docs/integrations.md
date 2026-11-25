# Integrations

[← vmono](../README.md)

By default, opening a session runs the harness in the terminal you are in and replaces vmono, so each agent needs a terminal of its own. An integration changes where the harness starts. The files each one installs live in [`integrations/`](../integrations/), are embedded in the binary, and are written out by `vmono integration install`, so they always match the version you run.

## tmux

```sh
vmono integration install tmux
tmux source-file ~/.tmux.conf     # or wherever the install says it wired
```

Every session gets a tmux session of its own, named `package/session`, with the harness as its only process. Opening a session creates it, or switches to it when it already exists, and never starts a second harness. Inside tmux the current client is switched; outside tmux vmono attaches. When the harness exits, tmux closes the session, as a direct launch returns you to your shell. Removing the vmono session (`session rm`, `finish --close`, `delete`) closes its tmux session too, unless vmono itself is running in it, in which case it says so and leaves it open.

The install also adds `prefix + V`, which opens the vmono TUI in a popup. Launching a session from it switches the client to that session's tmux session and closes the popup, so the TUI works as a control panel for every running agent.

| Does | Where |
| --- | --- |
| Writes the key bindings | `~/.config/vmono/integrations/tmux/vmono.tmux` |
| Adds one marked line that sources it | The first of `~/.tmux.conf`, `$XDG_CONFIG_HOME/tmux/tmux.conf` and `~/.config/tmux/tmux.conf` that exists, or `~/.tmux.conf` if none does. A symlinked file stays a symlink |
| Sets `launcher = "auto"` | `config.toml`, unless a launcher is already set or you pass `--no-launcher` |

Nothing is reloaded for you. `vmono integration uninstall tmux` removes the marked line and vmono's own directory and puts `launcher` back to `direct`; `vmono integration print tmux` prints the file if you would rather manage it in your dotfiles.

Switching is the default, but it moves your terminal out of the tmux session you were working in (it keeps running; `prefix + L` goes back, `prefix + s` lists them). Set `tmux_switch = false` to stay put: vmono still creates or finds the session, then shows `vmono: claude is running in tmux session "pkg/session" (prefix + s to switch)` in the tmux status line (and on standard error), and a TUI opened from the popup closes. Outside tmux the notice gives the `tmux attach` command instead, and nothing is attached. `VMONO_TMUX_SWITCH=false` does the same for one command.

Sessions are found by a `@vmono_session` tag that vmono sets on the tmux session, not by name, so renaming one in tmux does not lose it. A tmux session name cannot contain `.` or `:`, so those become `_`. Arguments after `--` in `vmono open` only apply when the session is created. tmux 3.0 or newer is needed. Native Windows has no tmux, so `auto` stays direct there.

## herdr

```sh
vmono integration install herdr
herdr server reload-config
```

[herdr](https://herdr.dev) is a terminal workspace manager for coding agents. Every session gets a herdr workspace of its own, labeled `package/session` and rooted at the session. Opening a session creates it, or goes to it when it already exists, and never starts a second harness. When the harness exits, its shell stays, and opening the session again starts the harness there. Removing the vmono session closes its workspace, unless vmono is running in it.

The install also binds `prefix + shift + v`, which opens the vmono TUI in a popup, by adding one marked block to herdr's config (`$HERDR_CONFIG_PATH`, else `~/.config/herdr/config.toml`). It refuses a key that config already uses; `--key` picks another. It sets `launcher = "auto"` as the tmux install does. `vmono integration uninstall herdr` removes the block. Keep your own settings above it: a key written after the block would belong to its table.

A launch from inside an agent does not move you; it prints the command that shows the workspace. Set `herdr_focus = false` (or `VMONO_HERDR_FOCUS=false` for one command) for the same on every launch. Sessions are found by a token vmono sets on the workspace, so renaming it does not lose it. After a herdr restart, which drops tokens, they are found by label and directory instead. Checked against herdr 0.9.5; not supported on Windows.

## Launchers

`launcher` takes `direct` (the default), `auto` (herdr or tmux when vmono runs inside one, direct otherwise), and `tmux` or `herdr` (always, and an error if it is missing). `VMONO_LAUNCHER` overrides it for one command, such as `VMONO_LAUNCHER=direct vmono open pkg/session` to see why a harness exits at once.
