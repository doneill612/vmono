# vmono

vmono manages **packages** and their **sessions**. A package is a set of git repos defined once, with a purpose that explains why they belong together. A session is one piece of work on a package: its own branch, and a directory holding a git worktree of every member, which a coding harness opens and sees all of them. It replaces the habit of copying repos out of `~/source/` into a temporary parent folder.

- A package is defined once. Starting more work on the same set of repos is a new session, not a new package, so nothing about the members is set up twice.
- Members are **git worktrees** of repos already on your machine: no copies, no re-clones, shared object storage.
- The package's purpose, context, notes and each member's role are written once and rendered into every session's `AGENTS.md` / `CLAUDE.md`.
- A harness opened at a session root works with no changes. Claude Code, OpenCode, Codex and Pi work out of the box; Claude Code and OpenCode also get generated config.
- A bundled skill teaches the harness running inside a session to read the package, and to write back to it only what every future session needs.
- Runs on macOS, Linux and Windows, without admin rights.

## Install

```sh
make install
```

This builds the binary and copies it to `~/.local/bin/vmono`; make sure that directory is on your `PATH`. Requires Go and git 2.30 or newer.

| Target | Does |
| --- | --- |
| `make build` | Compile to `bin/vmono` |
| `make install` | Build, then install to `~/.local/bin` (override with `PREFIX=` or `BINDIR=`) |
| `make uninstall` | Remove the installed binary |
| `make test` | Run `go vet` and the test suite |
| `make clean` | Remove `bin/` |

Without `make` (for example on Windows), `go install github.com/doneill612/vmono/cmd/vmono@latest` also works.

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

`vmono` with no arguments opens a full-screen console in the spirit of k9s: a header with your config and the keys for the current view, a framed table with its title in the border, breadcrumbs and notices along the bottom.

| View | Opened by | Shows |
| --- | --- | --- |
| packages | start, `:packages` | Every package: members (`2A 1R` = two active, one reference), session count, harness, clean or dirty (any session), last opened. A package from before sessions shows `migrate` |
| sessions | `enter` on a package, `:sessions [pkg]` | Per session: branch, clean or dirty, last opened |
| members | `m` on a session, `:members [pkg/session]` | Per member in that session: mode, branch or ref, HEAD, upstream, ahead/behind, dirty and untracked counts, role |
| describe | `d` | Purpose, context, notes, sessions and members for a package; add a session's branch and live state when opened from one; location and git state for a member |
| repos | `:repos` | Every known repo and how many packages use it; `enter` lists those packages |
| harnesses | `:harnesses` | Configured harnesses, whether each is on `PATH`, and the default |

Navigation is by drilling down: `enter` goes in, `esc` goes back. `:` opens a command bar with tab completion (`:sessions pay<tab>`, `:members pay/work`, `:repos`, `:harnesses`, `:config`, `:new`, `:quit`), `/` filters the current table, and `?` lists the keys for the view you are on. Nothing refreshes by itself: `ctrl+r` reloads the current view, and a view reloads when you come back to it.

| Key (packages) | Does |
| --- | --- |
| `enter` | Drill into the sessions |
| `o` / `l` | Open the most recently opened session with the default harness / pick a harness. A package with no sessions asks you to start one |
| `d` | Describe |
| `n` / `e` | New package / edit the selected one (applies to every session) |
| `s` / `f` | Sync every session / fetch every member's source |
| `ctrl+d` | Delete the package and all its sessions (asks first; typed confirmation if work would be lost in any of them) |
| `y` | Copy the package path |
| `N` `M` `O` `S` | Sort by name, members, opened, state; press again to reverse |

| Key (sessions) | Does |
| --- | --- |
| `enter` / `o` | Launch the default harness in the session |
| `l` | Pick a harness |
| `n` | Start a new session: a name, and optionally a branch (default `vmono/<package>/<session>`) |
| `m` / `d` | Members of the session / describe it |
| `s` / `f` | Sync the session / fetch every member's source |
| `e` | Edit the package |
| `ctrl+d` | Delete the session; the package and its other sessions stay |
| `y` | Copy the session path |

Creating a package ends at the new-session prompt, then the harness picker, so every path through the TUI still ends at a running harness except delete.

Repos that are not under a source root, or every repo when no source root is set, are found with the repo finder (`p` while choosing members, `a` in the repos view). Type a path and it lists the directories there, marks the git repos (`●`), and scans below a typed directory for repos nested deeper. `tab` completes, `enter` picks a repo or steps into a directory; picked repos are remembered.

## Concepts

| Term | Meaning |
| --- | --- |
| Package | A definition under the package root (default `~/.vmono`): the members, a purpose, context, notes and a role per member. It has its own small git repo, and holds no worktrees. |
| Session | One piece of work on a package: a directory with its own branch, its own git repo, and a worktree of every member. It is what a harness opens. A package has any number of them side by side. |
| Member | A source repo included in a package. Every session has a worktree of every member. |
| Active member | Checked out on the session branch (default `vmono/<package>/<session>`). The agent may edit it and commit to it. |
| Reference member | Checked out detached at a fixed ref under `_ref/`. Read-only context. |
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
│   ├── .claude/             generated: settings.json, skills
│   ├── opencode.json        generated
│   ├── member-a/            active member (worktree on vmono/project/token-refresh)
│   └── _ref/member-c/       reference member (detached worktree)
└── spike/                   another session, with its own branch and worktrees
```

Everything except the manifest, the session file and your own text in `AGENTS.md` (outside the `<!-- vmono:begin -->` / `<!-- vmono:end -->` markers) is rebuilt from the manifest. Changing the package (a note, a new member, an edit) applies to every session. Task details that belong to one piece of work stay in the harness conversation, and the bundled skill tells the agent to write to the package only what every future session will need. Packages and sessions cannot be renamed: harness session history is keyed by path.

A branch can be checked out in only one worktree at a time, and git cannot hold both `vmono/project` and `vmono/project/spike`, which is why session branches include the package name. If a branch name in the way already exists, vmono says so and suggests `--branch`; it never renames branches.

### Packages from before sessions

Earlier versions kept a package's branch and worktrees in the package itself. Such a package lists as needing migration, and `vmono doctor --fix` converts it into a package with one session named `main` (or `main-2` and so on if a member is already called `main`). The members' worktrees move into the session and are repaired in the source repos, your own text in `AGENTS.md` comes along, the session keeps the old branch, and the package's git history is kept. Harness history keyed by the old path, which Claude Code uses, does not follow the move, and the kept branch `vmono/<package>` means a new session needs `--branch` or a renamed old branch.

## Commands

Every read command takes `--json`. Anything that could discard work refuses unless given `--force`. Inside a session, the package and session arguments are optional. Commands that act on a session take `pkg/session`; a bare `pkg` works when it has exactly one session, and otherwise the error lists them.

| Command | Does |
| --- | --- |
| `vmono` | Open the TUI |
| `vmono init [--source-root P]... [--package-root P]` | Write the global config without prompts |
| `vmono ls` / `vmono repos` | List packages / known repos |
| `vmono new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--harness H] [--session S [--branch B] [--reuse-branch]]` | Define a package, and optionally start its first session |
| `vmono session new [pkg] <name> [--branch B] [--reuse-branch]` | Start a session: a worktree of every member on a new branch |
| `vmono session ls [pkg]` | List a package's sessions |
| `vmono session rm <pkg>/<session> [--force] [--delete-branches]` | Remove a session; the package and its other sessions stay |
| `vmono add [pkg] <repo> [--ref] [--role R]` | Add a member to the package, and so to every session (a known repo name or any path) |
| `vmono rm [pkg] <member> [--force] [--delete-branch]` | Remove a member from every session |
| `vmono edit [pkg]` | Edit the package manifest in `$EDITOR`; changes apply as a diff to every session |
| `vmono sync [pkg[/session]] [--refs]` | Recreate missing worktrees and regenerate files, in one session or all of a package's; `--refs` moves reference members to their latest fetched ref |
| `vmono status [pkg[/session]]` | Per member in a session: branch or ref, dirty, untracked, ahead/behind |
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

Exit codes: `0` success, `1` error, `2` refused because work would be lost, `3` not found. When a harness started with `vmono open` exits, vmono exits with its status.

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
```

Any CLI that takes its working directory as its project can be a harness. Precedence: flags, then `VMONO_*` environment variables, then the file, then defaults. `VMONO_CONFIG_DIR` and `VMONO_STATE_DIR` relocate the config and the index cache.

## Authentication

vmono runs the system `git` for everything and keeps no credentials of its own, so whatever lets `git fetch` work in a source repo works here: an SSH key in your agent, or a credential helper such as the one `gh auth setup-git` installs.

Fetches never prompt. Starting a session, adding members, sync and the TUI's fetch fetch each distinct source repo once, several at a time, and a repo that fails only warns and falls back to the refs already on disk. If a fetch fails for lack of credentials, vmono says so once, naming the repos. Load your key (`ssh-add`) or sign in through your credential helper, then retry. SSH runs in batch mode so it cannot prompt; set `GIT_SSH_COMMAND` or `core.sshCommand` if you need your own ssh command, and vmono will leave it alone.

## Safety

- A source repo's working tree and checked-out branch are never touched.
- A dirty worktree is never removed without `--force` (or typing the package or session name in the TUI). Removing a member, deleting a session or deleting a package checks every session it reaches, and one dirty worktree refuses the whole operation.
- A branch with unmerged commits is never deleted, even with `--force`.
- Deleting a package or session directory by hand leaves its worktrees registered in the source repos. `vmono doctor --fix` prunes those records, and starting a session and adding or editing members release any whose directory is gone before adding a worktree, warning about each. Branches are still kept, so recreating a session of the same name asks to reuse its branch. Prefer `vmono session rm` and `vmono delete`, which remove the worktrees properly.
- Symlinks are never followed or created.
- Committing from a worktree writes into the source repo's git directory, outside the package. `vmono inspect --json` lists each `git_common_dir` so sandboxing wrappers can allow it.

vmono enforces nothing about what an agent may do; the harness's own permission system does. The adapters add `ask` rules for `vmono rm`, `vmono session rm` and `vmono delete` and a deny rule for edits under `_ref/`.

## Development

```sh
go vet ./...
go test ./...
```

Integration tests build throwaway source repos with a bare remote in a temp directory and run real create, edit, sync, migrate and delete cycles. They never touch your config, state or git identity. CI runs on Linux, macOS and Windows.
