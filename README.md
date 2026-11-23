<p align="center">
  <img src="docs/logo.svg" alt="vmono logo" width="96" height="96"><br>
  <strong><code>vmono</code></strong><br>
  Many repos. One workspace for your coding agent.<br>
  <a href="https://github.com/doneill612/vmono/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/doneill612/vmono?style=flat-square" /></a>
  <a href="https://github.com/doneill612/vmono/actions/workflows/ci.yml"><img alt="Build status" src="https://img.shields.io/github/actions/workflow/status/doneill612/vmono/ci.yml?style=flat-square&label=ci" /></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/doneill612/vmono?style=flat-square" /></a><br>
  <img src="docs/vmono-ex.png" alt="The vmono TUI showing the packages view">
</p>

---

vmono manages **packages** and their **sessions**. A package is a set of git repos defined once, with a purpose that explains why they belong together. A session is one piece of work on a package: its own branch, and a directory holding a git worktree of every member, which a coding harness opens and sees all of them. It replaces the habit of copying repos out of `~/source/` into a temporary parent folder.

- A package is defined once. Starting more work on the same set of repos is a new session, not a new package, so nothing about the members is set up twice.
- Members are **git worktrees** of repos already on your machine: no copies, no re-clones, shared object storage.
- The package's purpose, context, notes and each member's role are written once and rendered into every session's `AGENTS.md` / `CLAUDE.md`.
- A harness opened at a session root works with no changes. Claude Code, OpenCode, Codex and Pi work out of the box; Claude Code and OpenCode also get generated config, and Codex and Pi get the skill.
- A skill teaches the harness running inside a session to read the package, and to write back to it only what every future session needs. Claude Code gets it as a [plugin](#the-claude-code-plugin) that also adds commands and live session state; other harnesses get the same skill on its own.
- Runs on macOS, Linux and Windows, without admin rights.

## Install

macOS and Linux:

```sh
curl -fsSL https://github.com/doneill612/vmono/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/doneill612/vmono/releases/latest/download/install.ps1 | iex
```

The script downloads the archive for your OS and CPU from the latest [release](https://github.com/doneill612/vmono/releases), checks it against the release's `checksums.txt`, and installs `vmono` to `~/.local/bin` (`%LOCALAPPDATA%\Programs\vmono\bin` on Windows, which it adds to your user `PATH`). On macOS and Linux, make sure `~/.local/bin` is on your `PATH`. Requires git 2.30 or newer.

| Variable | Does |
| --- | --- |
| `VMONO_VERSION` | Install a specific release, e.g. `v0.1.0`, instead of the latest. On macOS and Linux you can also pass it as an argument: `... \| sh -s v0.1.0` |
| `VMONO_INSTALL_DIR` | Install somewhere else |
| `VMONO_DOWNLOAD_BASE` | Download from a mirror of the releases page |

To uninstall, delete the `vmono` binary (and, on Windows, its folder from your `PATH`; the PowerShell commands are under *From source* below). To check what you have, run `vmono --version`.

A tarball downloaded in a browser on macOS is quarantined by Gatekeeper; clear it with `xattr -d com.apple.quarantine vmono`. The install script doesn't need this.

### From source

Requires [Go](https://go.dev/dl/) 1.25 or newer and git 2.30 or newer. Every route below installs the same binary; pick the one for your platform.

**macOS and Linux** (and WSL), with `make`:

```sh
git clone https://github.com/doneill612/vmono
cd vmono
make install
```

This builds the binary and copies it to `~/.local/bin/vmono`. Override the location with `PREFIX=` or `BINDIR=`.

**Windows**, in PowerShell. `make` is not normally installed on Windows, so these are the same steps by hand:

```powershell
git clone https://github.com/doneill612/vmono
cd vmono

# Build, stamped with the git version
$version = git describe --tags --always --dirty 2>$null; if (-not $version) { $version = 'dev' }
go build -trimpath -ldflags "-X github.com/doneill612/vmono/internal/buildinfo.version=$version" -o bin\vmono.exe .\cmd\vmono

# Install to the same folder as the install script, and add it to your user PATH
$dir = Join-Path $env:LOCALAPPDATA 'Programs\vmono\bin'
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item bin\vmono.exe $dir -Force
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    $newPath = (@($userPath, $dir) | Where-Object { $_ }) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
}
```

This installs to `%LOCALAPPDATA%\Programs\vmono\bin`, the same place as the install script, so either one can replace the other. Open a new terminal afterwards so the `PATH` change takes effect.

**Any OS, without cloning**, with `go install`:

```sh
go install github.com/doneill612/vmono/cmd/vmono@latest
```

Go puts the binary in `$(go env GOPATH)/bin` (`%USERPROFILE%\go\bin` on Windows), which must be on your `PATH`. The version is read from the module, so `vmono --version` reports the tag.

The rest of the Makefile, on macOS and Linux:

```sh
make build      # build to bin/vmono
make test       # go vet, then go test
make uninstall  # remove the installed binary
make clean      # remove bin/ and dist/
make snapshot   # dry-run the release build (needs GoReleaser)
```

And the same tasks on Windows, in PowerShell from the repo root:

```powershell
# Build to bin\vmono.exe (make build)
$version = git describe --tags --always --dirty 2>$null; if (-not $version) { $version = 'dev' }
go build -trimpath -ldflags "-X github.com/doneill612/vmono/internal/buildinfo.version=$version" -o bin\vmono.exe .\cmd\vmono

# Test (make test)
go vet ./...
go test ./...

# Uninstall (make uninstall): remove the binary and its folder from your user PATH
$dir = Join-Path $env:LOCALAPPDATA 'Programs\vmono\bin'
Remove-Item (Join-Path $dir 'vmono.exe') -ErrorAction SilentlyContinue
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$newPath = (($userPath -split ';') | Where-Object { $_ -and $_ -ne $dir }) -join ';'
[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')

# Clean (make clean)
Remove-Item -Recurse -Force bin, dist -ErrorAction SilentlyContinue

# Dry-run the release build (make snapshot); needs GoReleaser
goreleaser release --snapshot --clean
```

If you installed somewhere else with `VMONO_INSTALL_DIR`, set `$dir` to that folder before the uninstall step. [GoReleaser](https://goreleaser.com) is only needed for the dry run.

### Releasing

Push a version tag. CI runs the tests, publishes the release with GoReleaser, then installs it on Linux, macOS and Windows with the install scripts:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

A tag with a suffix, such as `v0.2.0-rc.1`, is published as a prerelease, which `latest` skips.

## Quick start

```sh
vmono                      # opens the TUI; first run walks you through setup
```

Or from the command line:

```sh
vmono init --source-root ~/source
vmono new project --active member-a,member-b --ref member-c \
    --purpose-file purpose.md --session token-refresh
vmono open project/token-refresh   # launches your default harness at the session root
cd "$(vmono path project/token-refresh)" && claude    # same thing, by hand

vmono session new project spike    # more work on the same repos: a new session
```

Add `eval "$(vmono shell-init zsh)"` (or `bash`, `fish`, `pwsh`) to your shell rc for a `vcd <pkg>[/session]` function.

## The TUI

`vmono` with no arguments opens a full-screen console. A one-line bar at the top shows where you are (`packages › payments › work`) and how many packages there are; a one-line footer shows the few keys worth knowing and any notice. Between them is the list, and on a terminal 100 columns or wider a preview of the highlighted row sits beside it: a package's purpose, sessions and members, a session's branch and the live state of each member.

You rarely need a key. **`enter` opens a menu of everything you can do to the highlighted row**, with the most common action first: `Sessions` for a package, `Launch` for a session. Each entry shows its shortcut, and pressing that key from the list (or inside the menu) does the same thing, so the menu is also how you learn them. `?` lists every shortcut for the view you are on.

| View | Opened by | Shows |
| --- | --- | --- |
| packages | start | Every package: members (`2A 1R` = two active, one reference), session count, harness, clean or dirty (any session), last opened. A package from before sessions shows `migrate` |
| sessions | `→` on a package | Per session: branch, clean, dirty or finished (with when), last opened. Finished sessions are dimmed and sorted below the rest |
| members | `m` on a session | Per member in that session: mode, branch or ref, HEAD, upstream, ahead/behind, dirty and untracked counts, role |
| describe | `d` | Purpose, context, notes, sessions and members for a package; add a session's branch and live state when opened from one; location and git state for a member |
| repos | palette | Every known repo and how many packages use it; `→` lists those packages |
| harnesses | palette | Configured harnesses, whether each is on `PATH`, and the default |

`esc` goes back. `:` (or `ctrl+p`) opens the command palette, a fuzzy-filtered list of everything you can do from where you are: go to a view (`repos`, `harnesses`, `settings`, `new package`, `sessions of <package>`), any action on the highlighted row, sort the list by a column, reload, quit. `/` filters the current list as you type. Nothing refreshes by itself: `ctrl+r` reloads the current view, and a view reloads when you come back to it.

The shortcuts, none of which you have to use:

| Key (packages) | Does |
| --- | --- |
| `enter` | Open the menu |
| `→` | Drill into the sessions |
| `o` / `l` | Open the most recently opened session with the default harness / pick a harness. A package with no sessions asks you to start one |
| `d` | Describe |
| `n` / `e` | New package / edit the selected one (applies to every session) |
| `s` / `f` | Sync every session / fetch every member's source |
| `ctrl+d` | Delete the package and all its sessions (asks first; typed confirmation if work would be lost in any of them) |
| `y` | Copy the package path |
| `c` | Edit the config (package root, source roots, default harness) |

| Key (sessions) | Does |
| --- | --- |
| `enter` | Open the menu: launch, launch with…, members, describe, sync, fetch, copy path, finish, push, delete |
| `o` | Launch the default harness in the session |
| `l` | Pick a harness |
| `n` | Start a new session: a name, optionally a branch (default `vmono/<package>/<session>`), and one `into` row per active member, filled in with the branch its source has checked out |
| `F` | Finish the session: shows what would land on which branch, or what blocks it, then `y` lands it. `i` changes the members' landing branches, `c` also removes the session afterward. A conflict offers to launch the harness in the session to resolve it |
| `P` | Push a finished session: shows each member's landing branch and the remote it goes to, or what blocks it, then `y` pushes. Never forced. An in-progress session is refused |
| `m` / `d` | Members of the session / describe it |
| `s` / `f` | Sync the session / fetch every member's source |
| `e` | Edit the package |
| `ctrl+d` | Delete the session; the package and its other sessions stay |
| `y` | Copy the session path |

| Key (members) | Does |
| --- | --- |
| `p` | Push the highlighted member's landing branch (a finished session; same dialog as `P`, for one member) |
| `P` | Push every member of the finished session |

Members, repos and harnesses have menus too (`enter`), with the same keys where they apply. The members preview shows where each landing branch is pushed and how many commits it has that the remote lacks, and a finished session's preview sums them. Sorting is only in the palette (`sort by name`, `sort by opened`, …); choosing the same column again reverses it.

Creating a package returns to the packages screen; start a session from there (`enter`, then `New session`, or `→` then `n`) when you want one.

Repos that are not under a source root, or every repo when no source root is set, are found with the repo finder (`p` while choosing members, `a` in the repos view). Type a path and it lists the directories there, marks the git repos (`●`), and scans below a typed directory for repos nested deeper. `tab` completes, `enter` picks a repo or steps into a directory; picked repos are remembered.

## Concepts

| Term | Meaning |
| --- | --- |
| Package | A definition under the package root (default `~/.vmono`): the members, a purpose, context, notes and a role per member. It has its own small git repo, and holds no worktrees. |
| Session | One piece of work on a package: a directory with its own branch, its own git repo, and a worktree of every member. It is what a harness opens. A package has any number of them side by side. |
| Member | A source repo included in a package. Every session has a worktree of every member. |
| Active member | Checked out on the session branch (default `vmono/<package>/<session>`). The agent may edit it and commit to it. |
| Reference member | Checked out detached at a fixed ref under `_ref/`. Read-only context. |
| Landing branch | The branch in a member's source repo that the session's work lands on when it is finished. Each member has its own, recorded in the session when it is created: by default the branch that member's source repo has checked out. |
| Purpose / Context / Notes / Role | Free text that belongs to the package: what the work is, what to know, a dated log, and what each member contributes. Sessions share it; they have none of their own. |

A package with two sessions looks like this:

```text
~/.vmono/project/
├── .vmono/manifest.toml     source of truth for the package
├── .git/  .gitignore        the package's own repo: tracks only .vmono/
├── token-refresh/           a session: what the harness opens
│   ├── .vmono/session.toml  name, package, branch
│   ├── .git/                the session's own repo (one harness project per session)
│   ├── AGENTS.md            generated block + your own notes
│   ├── CLAUDE.md            generated: @AGENTS.md
│   ├── .claude/             generated: settings.json, skills/vmono (the Claude Code plugin)
│   ├── .agents/             generated: skills/vmono, for Codex and Pi
│   ├── opencode.json        generated
│   ├── member-a/            active member (worktree on vmono/project/token-refresh)
│   └── _ref/member-c/       reference member (detached worktree)
└── spike/                   another session, with its own branch and worktrees
```

Everything except the manifest, the session file and your own text in `AGENTS.md` (outside the `<!-- vmono:begin -->` / `<!-- vmono:end -->` markers) is rebuilt from the manifest. Changing the package (a note, a new member, an edit) applies to every session. Task details that belong to one piece of work stay in the harness conversation, and the vmono skill tells the agent to write to the package only what every future session will need. Packages and sessions cannot be renamed: harness session history is keyed by path.

A branch can be checked out in only one worktree at a time, and git cannot hold both `vmono/project` and `vmono/project/spike`, which is why session branches include the package name. If a branch name in the way already exists, vmono says so and suggests `--branch`; it never renames branches.

## Finishing a session

A session is done when its work has landed on a branch in each member's source repo. `vmono session finish` does that locally; it never pushes. Pushing is a separate step you ask for, below.

1. For each active member, the session's own commits (on the session branch, but on neither the member's base nor its landing branch) are rebased onto the landing branch, inside the session's worktree.
2. The landing branch in the source repo then moves to the result: fast-forwarded when it is checked out in a clean worktree, updated in place when nothing has it checked out, and created when it does not exist yet.

Members with no commits of their own are skipped. Landing branches are per member, since repos need not share a branch name. When a session is created, each member's is recorded as the branch its source repo has checked out at that moment (a source on a detached HEAD falls back to the branch its base names). Override them at create with `--into BRANCH` for every member or `--into MEMBER=BRANCH` for one (repeatable), or in the `into` rows of the TUI prompt, which start filled in. Change them later with `vmono session into [pkg/session] --all B --member NAME=B`, with `i` in the TUI finish dialog, or with `--into` on `finish`; `vmono session into` with no flags lists them. A branch that does not exist yet is created when the session lands.

When a member's landing branch is not the branch its base names (a feature branch, say) and it exists in the source, the session starts from it, so the work builds on that branch and lands back on it. When it is the base's branch, the session starts from the base (`origin/main`) as usual, so a stale local `main` is never the start point. A session started with the source on a stray branch lands there, so check the `into` rows before starting; the finish dialog shows each member's target too.

Nothing moves unless every member can land. Uncommitted or untracked files, a landing branch checked out with uncommitted changes, or a rebase already in progress each refuse the finish (exit code `2`, with the reasons; `--json` lists them per member). If a rebase stops on conflicts, they stay in that member's worktree: resolve them, `git add`, `git rebase --continue`, and run `vmono session finish` again. Members already rebased are found up to date. If moving the branches fails partway, the ones already moved are put back.

A finished session is not a removed one. It stays in the package's session list, dimmed and sorted below the others, with its worktrees and branch, until you remove it (`ctrl+d` in the TUI, `vmono session rm`, or `finish --close` to do both at once). Its state is derived rather than stored: any new commit or uncommitted edit makes it `in-progress` again, relaunching it changes nothing, and finishing again lands only the new commits. `vmono session ls`, `status` and `show --json` report the state, and `status` shows what is left to land in each member.

### Pushing a finished session

Landing leaves each member's landing branch ahead of its remote. `vmono session push [pkg/session] [--member NAME]... [--dry-run] [--json]`, or `P` on the session (or `p` on a member) in the TUI, publishes them. It is only for a finished session: one that was never finished, or has new commits or edits since, is refused with exit code `2`, so nothing unlanded or half-committed is pushed.

Only each member's landing branch is pushed, from its source repo, and never the session's own branch. A branch that tracks a remote branch goes there. A branch with no upstream goes to a branch of the same name on `origin` (or on the repo's only remote) and is set to track it, which is how a landing branch that finish created ends up published. A repo with several remotes and no `origin` blocks that member until you give the branch an upstream with `git branch --set-upstream-to`.

Nothing is forced. If the remote branch has commits the landing branch lacks, the member is blocked, or git refuses the push, and you pull or rebase in the member's source repo and push again. Whether a branch is behind is judged from the last fetch (`f` in the TUI, or `git fetch` in the source repo), so the dialog can be a fetch out of date, and git has the final say. Members are independent: one that is blocked or refused does not stop the others, and the command exits `2` when any is blocked (`1` when git refused one). Members whose remote already has everything are skipped, so pushing twice is safe. `--dry-run` prints the plan and pushes nothing.

Push before you remove the session: `finish --close` and `vmono session rm` take the session with them, after which the landing branches are plain branches in the source repos and you push them by hand.

The vmono skill teaches the agent to do this when you say the work is done (in Claude Code, `/vmono:finish` says it for you): commit in every member, run `vmono session finish`, resolve a conflict if one stops it, and report what landed where. The adapters ask for permission first, and the agent never passes `--close` or pushes; pushing is yours, from the TUI or `vmono session push`.

### Packages from before sessions

Earlier versions kept a package's branch and worktrees in the package itself. Such a package lists as needing migration, and `vmono doctor --fix` converts it into a package with one session named `main` (or `main-2` and so on if a member is already called `main`). The members' worktrees move into the session and are repaired in the source repos, your own text in `AGENTS.md` comes along, the session keeps the old branch, and the package's git history is kept. Harness history keyed by the old path, which Claude Code uses, does not follow the move, and the kept branch `vmono/<package>` means a new session needs `--branch` or a renamed old branch.

## The Claude Code plugin

For Claude Code, vmono writes a plugin into every session at `.claude/skills/vmono/`. It is a skills-directory plugin: Claude Code loads a folder there that holds a `.claude-plugin/plugin.json` in place, with no marketplace and no install step, once you have trusted the session folder. It is rebuilt from the vmono binary on every launch and sync, so it never drifts from the version you run, and it is not meant to be edited.

| Part | Does |
| --- | --- |
| `SKILL.md` (`/vmono`) | The skill every harness gets: how to recognize a session, read the package, and write back to it |
| SessionStart hook | Runs `vmono hook session-start` when a conversation starts, resumes, is cleared, forked or compacted, and adds the session's live state to the context: each member's branch, uncommitted files, and commits left to land |
| `/vmono:status` | Summarizes that state on request, including anything that would block finishing |
| `/vmono:note [text]` | Records a package note from your text; with none, proposes one from the conversation and asks first |
| `/vmono:finish` | Lands the session: commit in every member, `vmono session finish`, resolve a conflict if one stops it, report what landed. Claude Code still asks for permission to run `vmono session finish` |

The hook runs the bare command `vmono`, so `vmono` must be on `PATH` for the harness, as the skill's own commands already assume. It prints nothing outside a session and never fails a start.

If your organization blocks skills-directory plugins, or your Claude Code predates them, `/vmono` still loads as an ordinary project skill and only the hook and the three commands are missing. To turn the plugin off for one session, set `"vmono@skills-dir": false` under `enabledPlugins` in that session's `.claude/settings.local.json`, or run `claude plugin disable vmono@skills-dir`.

The other harnesses have no plugin strategy yet, so they get the same `SKILL.md` on its own, with the guidance behind the three commands (checking status, proposing a note before writing it, finishing) in it: OpenCode under `.opencode/skills/vmono/`, and Codex and Pi under `.agents/skills/vmono/`, which both scan from the session root. They do not get the live state at the start of a conversation; the skill tells the agent to run `vmono show --json` and `vmono status --json` instead.

## Commands

Every read command takes `--json`. Anything that could discard work refuses unless given `--force`. Inside a session, the package and session arguments are optional. Commands that act on a session take `pkg/session`; a bare `pkg` works when it has exactly one session, and otherwise the error lists them.

| Command | Does |
| --- | --- |
| `vmono` | Open the TUI |
| `vmono init [--source-root P]... [--package-root P]` | Write the global config without prompts |
| `vmono ls` / `vmono repos` | List packages / known repos |
| `vmono new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--harness H] [--session S [--branch B] [--into [MEMBER=]B]... [--reuse-branch]]` | Define a package, and optionally start its first session |
| `vmono session new [pkg] <name> [--branch B] [--into [MEMBER=]B]... [--reuse-branch]` | Start a session: a worktree of every member on a new branch. Records each member's landing branch (default: its source's checked-out branch) |
| `vmono session into [pkg/session] [--all B] [--member NAME=B]...` | Show each member's landing branch, or change them |
| `vmono session ls [pkg]` | List a package's sessions and whether each is finished |
| `vmono session finish [pkg/session] [--into [MEMBER=]B]... [--close] [--dry-run]` | Land the session's commits on each member's branch in its source repo. Nothing is pushed; see Finishing a session |
| `vmono session push [pkg/session] [--member NAME]... [--dry-run] [--json]` | Push a finished session's landing branches to their remotes, never forced. See Pushing a finished session |
| `vmono session rm <pkg>/<session> [--force] [--delete-branches]` | Remove a session; the package and its other sessions stay |
| `vmono add [pkg] <repo> [--ref] [--role R]` | Add a member to the package, and so to every session (a known repo name or any path) |
| `vmono rm [pkg] <member> [--force] [--delete-branch]` | Remove a member from every session |
| `vmono edit [pkg]` | Edit the package manifest in `$EDITOR`; changes apply as a diff to every session |
| `vmono sync [pkg[/session]] [--refs]` | Recreate missing worktrees and regenerate files, in one session or all of a package's; `--refs` moves reference members to their latest fetched ref |
| `vmono status [pkg[/session]]` | The session's state, and per member: branch or ref, dirty, untracked, ahead/behind, and what is left to land |
| `vmono show [pkg[/session]]` | Purpose, context, notes, sessions and members; with a session, each member's live state |
| `vmono describe [pkg] --purpose T \| --context T` | Replace the purpose or context (`--purpose-file`, `--context-file` too) |
| `vmono role [pkg] <member> TEXT` | Set a member's role |
| `vmono note [pkg] TEXT` | Append a note to the package, stamped with time and author |
| `vmono open [pkg[/session]] [--harness H] [-- ARGS]` | Launch a harness at a session root |
| `vmono path [pkg[/session]]` | Print a session's path, or the package's when no session is meant |
| `vmono delete <pkg> [--force] [--delete-branches]` | Tear down a package and every one of its sessions |
| `vmono inspect [pkg[/session]]` | A session's full resolved model as JSON, including each member's `git_common_dir` |
| `vmono doctor [--fix]` | Find drift, migrate packages from before sessions, and rebuild the index |
| `vmono shell-init zsh\|bash\|fish\|pwsh` | Print a `vcd <pkg>[/session]` function |

Exit codes: `0` success, `1` error, `2` refused because work would be lost (or, for `session finish`, because a member cannot land yet), `3` not found. When a harness started with `vmono open` exits, vmono exits with its status.

## Configuration

`~/.config/vmono/config.toml` (`%APPDATA%\vmono\config.toml` on Windows):

```toml
source_roots    = ["~/source"]              # scanned to fill the repo picker
repos           = ["~/work/one-off-repo"]   # individual repos, from anywhere
package_root    = "~/.vmono"
scan_depth      = 2
branch_prefix   = "vmono/"
fetch_on_create = true      # fetch source repos before adding worktrees; failures only warn
default_harness = "claude"
auto_commit     = true      # commit every package change to the package's own repo
return_to_tui   = false     # always true on Windows

[harness.claude]
command = "claude"
adapter = "claude-code"

[harness.codex]
command = "codex"
adapter = "agent-skills"
```

Any CLI that takes its working directory as its project can be a harness. The `adapter` says what vmono generates for it: `claude-code` (settings and the plugin), `opencode` (`opencode.json` and the skill), `agent-skills` (the skill under `.agents/skills/`, for Codex and Pi), or none. A built-in harness listed without an adapter still running its built-in command gets its default one, so a config saved before an adapter existed picks it up. Precedence: flags, then `VMONO_*` environment variables, then the file, then defaults. `VMONO_CONFIG_DIR` and `VMONO_STATE_DIR` relocate the config and the index cache.

## Authentication

vmono runs the system `git` for everything and keeps no credentials of its own, so whatever lets `git fetch` work in a source repo works here: an SSH key in your agent, or a credential helper such as the one `gh auth setup-git` installs.

Fetches and pushes never prompt. Starting a session, adding members, sync and the TUI's fetch fetch each distinct source repo once, several at a time, and a repo that fails only warns and falls back to the refs already on disk. If a fetch fails for lack of credentials, vmono says so once, naming the repos. Load your key (`ssh-add`) or sign in through your credential helper, then retry. SSH runs in batch mode so it cannot prompt; set `GIT_SSH_COMMAND` or `core.sshCommand` if you need your own ssh command, and vmono will leave it alone.

## Safety

- A source repo's working tree and checked-out branch are never touched, with one exception: `vmono session finish` fast-forwards a landing branch that is checked out in a clean worktree, and refuses when that worktree has uncommitted changes.
- A dirty worktree is never removed without `--force` (or typing the package or session name in the TUI). Removing a member, deleting a session or deleting a package checks every session it reaches, and one dirty worktree refuses the whole operation.
- A branch with unmerged commits is never deleted, even with `--force`. A session branch counts as merged when it is merged into the member's base or into its landing branch, and commits already on the landing branch are not counted as unpushed.
- `vmono session finish` moves no branch unless every member can land, and puts back the ones it moved if a later one fails.
- `vmono session push` is the only command that pushes, and only on request. It sends one branch per member, the landing branch, never forced, and refuses a session that is not finished. A branch the remote has moved past is left alone.
- Deleting a package or session directory by hand leaves its worktrees registered in the source repos. `vmono doctor --fix` prunes those records, and starting a session and adding or editing members release any whose directory is gone before adding a worktree, warning about each. Branches are still kept, so recreating a session of the same name asks to reuse its branch. Prefer `vmono session rm` and `vmono delete`, which remove the worktrees properly.
- On Windows, vmono refuses to remove a session, member or package while your working directory is inside it, since Windows cannot delete a directory in use. `cd` out and run the command again, naming the session (`vmono session finish PKG/SESSION --close`). Nothing has changed when it refuses, and `--force` does not apply. Another program holding the directory open, such as an editor or a second terminal, can still make the removal fail with git's own `Permission denied`.
- Symlinks are never followed or created.
- Committing from a worktree writes into the source repo's git directory, outside the package. `vmono inspect --json` lists each `git_common_dir` so sandboxing wrappers can allow it.

vmono enforces nothing about what an agent may do; the harness's own permission system does. The `claude-code` and `opencode` adapters add `ask` rules for `vmono rm`, `vmono session rm`, `vmono session finish` and `vmono delete` and a deny rule for edits under `_ref/`.

## Development

```sh
go vet ./...
go test ./...
```

Integration tests build throwaway source repos with a bare remote in a temp directory and run real create, edit, sync, migrate and delete cycles. They never touch your config, state or git identity. CI runs on Linux, macOS and Windows.
