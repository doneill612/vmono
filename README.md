# vmono

vmono manages **packages**: directories that bring several git repos together so a coding harness opened at the package root sees all of them, along with a generated context file that explains why they belong together. It replaces the habit of copying repos out of `~/source/` into a temporary parent folder.

- Members are **git worktrees** of repos already on your machine: no copies, no re-clones, shared object storage.
- The package's purpose and each member's role are written once and rendered into `AGENTS.md` / `CLAUDE.md`.
- A harness opened at the package root works with no changes. Claude Code, OpenCode, Codex and Pi work out of the box; Claude Code and OpenCode also get generated config.
- A bundled skill teaches the harness running inside a package to read and update its context.
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
    --purpose-file purpose.md
vmono open project   # launches your default harness at the package root
cd "$(vmono path project)" && claude    # same thing, by hand
```

Add `eval "$(vmono shell-init zsh)"` (or `bash`, `fish`, `pwsh`) to your shell rc for a `vcd <pkg>` function.

## The TUI

`vmono` with no arguments opens a full-screen console in the spirit of k9s: a header with your config and the keys for the current view, a framed table with its title in the border, breadcrumbs and notices along the bottom.

| View | Opened by | Shows |
| --- | --- | --- |
| packages | start, `:packages` | Every package: members (`2A 1R` = two active, one reference), branch, harness, clean or dirty, last opened |
| members | `enter` on a package, `:members [pkg]` | Per member: mode, branch or ref, HEAD, upstream, ahead/behind, dirty and untracked counts, role |
| describe | `d` | Purpose, context, notes and paths for a package; location and git state for a member |
| repos | `:repos` | Every known repo and how many packages use it; `enter` lists those packages |
| harnesses | `:harnesses` | Configured harnesses, whether each is on `PATH`, and the default |

Navigation is by drilling down: `enter` goes in, `esc` goes back. `:` opens a command bar with tab completion (`:members pay<tab>`, `:repos`, `:harnesses`, `:config`, `:new`, `:quit`), `/` filters the current table, and `?` lists the keys for the view you are on. Nothing refreshes by itself: `ctrl+r` reloads the current view, and a view reloads when you come back to it.

| Key (packages) | Does |
| --- | --- |
| `enter` | Drill into the members |
| `o` / `l` | Open with the default harness / pick a harness |
| `d` | Describe |
| `n` / `e` | New package / edit the selected one |
| `s` / `f` | Sync / fetch every member's source |
| `ctrl+d` | Delete (asks first; typed confirmation if work would be lost) |
| `y` | Copy the package path |
| `N` `M` `O` `S` | Sort by name, members, opened, state; press again to reverse |

Repos that are not under a source root, or every repo when no source root is set, are found with the repo finder (`p` while choosing members, `a` in the repos view). Type a path and it lists the directories there, marks the git repos (`●`), and scans below a typed directory for repos nested deeper. `tab` completes, `enter` picks a repo or steps into a directory; picked repos are remembered.

## Concepts

| Term | Meaning |
| --- | --- |
| Package | A managed directory under the package root (default `~/.vmono`) that groups members for one piece of work. It has its own small git repo. |
| Member | A source repo included in a package, as a git worktree. |
| Active member | Checked out on the package branch (`vmono/<package>`). The agent may edit it and commit to it. |
| Reference member | Checked out detached at a fixed ref under `_ref/`. Read-only context. |
| Purpose / Role | Free text saying what the work is, and what each member contributes. |

A package looks like this:

```text
~/.vmono/project/
├── .vmono/manifest.toml     source of truth
├── AGENTS.md                generated block + your own notes
├── CLAUDE.md                generated: @AGENTS.md
├── .claude/                 generated: settings.json, skills
├── opencode.json            generated
├── member-a/                active member (worktree)
└── _ref/member-c/           reference member (detached worktree)
```

Everything except the manifest and your own text in `AGENTS.md` (outside the `<!-- vmono:begin -->` / `<!-- vmono:end -->` markers) is rebuilt from the manifest. Packages cannot be renamed: harness session history is keyed by path.

## Commands

Every read command takes `--json`. Anything that could discard work refuses unless given `--force`. Inside a package, the package argument is optional.

| Command | Does |
| --- | --- |
| `vmono` | Open the TUI |
| `vmono init [--source-root P]... [--package-root P]` | Write the global config without prompts |
| `vmono ls` / `vmono repos` | List packages / known repos |
| `vmono new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--branch B] [--harness H] [--reuse-branch]` | Create a package |
| `vmono add [pkg] <repo> [--ref] [--role R]` | Add a member (a known repo name or any path) |
| `vmono rm [pkg] <member> [--force] [--delete-branch]` | Remove a member |
| `vmono edit [pkg]` | Edit the manifest in `$EDITOR`; changes apply as a diff |
| `vmono sync [pkg] [--refs]` | Recreate missing worktrees and regenerate files; `--refs` moves reference members to their latest fetched ref |
| `vmono status [pkg]` | Per member: branch or ref, dirty, untracked, ahead/behind |
| `vmono show [pkg]` | Purpose, context, notes and members |
| `vmono describe [pkg] --purpose T \| --context T` | Replace the purpose or context (`--purpose-file`, `--context-file` too) |
| `vmono role [pkg] <member> TEXT` | Set a member's role |
| `vmono note [pkg] TEXT` | Append a note stamped with time and author |
| `vmono open [pkg] [--harness H] [-- ARGS]` | Launch a harness at the package root |
| `vmono path [pkg]` | Print the package path |
| `vmono delete <pkg> [--force] [--delete-branches]` | Tear down a package |
| `vmono inspect [pkg]` | Full resolved model as JSON, including each member's `git_common_dir` |
| `vmono doctor [--fix]` | Find drift and rebuild the index |
| `vmono shell-init zsh\|bash\|fish\|pwsh` | Print a `vcd <pkg>` function |

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
auto_commit     = true      # commit every manifest change to the package's own repo
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

Fetches never prompt. Create, edit, sync and the TUI's fetch fetch each distinct source repo once, several at a time, and a repo that fails only warns and falls back to the refs already on disk. If a fetch fails for lack of credentials, vmono says so once, naming the repos. Load your key (`ssh-add`) or sign in through your credential helper, then retry. SSH runs in batch mode so it cannot prompt; set `GIT_SSH_COMMAND` or `core.sshCommand` if you need your own ssh command, and vmono will leave it alone.

## Safety

- A source repo's working tree and checked-out branch are never touched.
- A dirty worktree is never removed without `--force` (or typing the package name in the TUI).
- A branch with unmerged commits is never deleted, even with `--force`.
- Symlinks are never followed or created.
- Committing from a worktree writes into the source repo's git directory, outside the package. `vmono inspect --json` lists each `git_common_dir` so sandboxing wrappers can allow it.

vmono enforces nothing about what an agent may do; the harness's own permission system does. The adapters add `ask` rules for `vmono rm` and `vmono delete` and a deny rule for edits under `_ref/`.

## Development

```sh
go vet ./...
go test ./...
```

Integration tests build throwaway source repos with a bare remote in a temp directory and run real create, edit, sync and delete cycles. They never touch your config, state or git identity. CI runs on Linux, macOS and Windows.
