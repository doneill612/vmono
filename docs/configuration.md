# Configuration

[← vmono](../README.md)

`~/.config/vmono/config.toml` (`%APPDATA%\vmono\config.toml` on Windows):

```toml
source_roots    = ["~/source"]              # scanned to fill the repo picker
repos           = ["~/work/one-off-repo"]   # individual repos, from anywhere
package_root    = "~/.vmono"
scan_depth      = 2
fetch_on_create = true      # fetch source repos before adding worktrees (a new session always does); failures only warn
default_harness = "claude"
auto_commit     = true      # commit every package change to the package's own repo
return_to_tui   = false     # always true on Windows
launcher        = "direct"  # or "auto" / "tmux" / "herdr": where a session's harness starts (see Integrations)
tmux_switch     = true      # tmux launcher: switch to the session's tmux session; false only notifies
herdr_focus     = true      # herdr launcher: focus the session's herdr workspace; false only notifies

[harness.claude]
command = "claude"
adapter = "claude-code"

[harness.codex]
command = "codex"
adapter = "agent-skills"
```

Precedence: flags, then `VMONO_*` environment variables, then the file, then defaults. `VMONO_CONFIG_DIR` and `VMONO_STATE_DIR` relocate the config and the index cache.

`vmono init` writes this file without prompts; the TUI edits it with `c` on the packages view. [Hooks](setup-and-teardown.md#hooks) are set here too, and `launcher` and its companions are covered in [Integrations](integrations.md#launchers).

## Harnesses

Any CLI that takes its working directory as its project can be a harness. The `adapter` says what vmono generates for it:

| Adapter | Generates |
| --- | --- |
| `claude-code` | Settings and the [plugin](claude-code-plugin.md) |
| `opencode` | `opencode.json` and the skill |
| `agent-skills` | The skill under `.agents/skills/`, for Codex and Pi |
| none | Nothing beyond `AGENTS.md` |

A built-in harness listed without an adapter still running its built-in command gets its default one, so a config saved before an adapter existed picks it up. `init_args` are added when a harness is started to [initialize a package](package-init.md), with `{prompt}` replaced by the first prompt. Built-in harnesses have defaults; without any, vmono prints the prompt for you to paste.

## Authentication

vmono runs the system `git` for everything and keeps no credentials of its own, so whatever lets `git fetch` work in a source repo works here: an SSH key in your agent, or a credential helper such as the one `gh auth setup-git` installs.

Fetches and pushes never prompt. Starting a session, adding members, sync and the TUI's fetch fetch each distinct source repo once, several at a time, and a repo that fails only warns and falls back to the refs already on disk. If a fetch fails for lack of credentials, vmono says so once, naming the repos. Load your key (`ssh-add`) or sign in through your credential helper, then retry. SSH runs in batch mode so it cannot prompt; set `GIT_SSH_COMMAND` or `core.sshCommand` if you need your own ssh command, and vmono will leave it alone.
