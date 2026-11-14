# vmono

vmono manages **packages**: directories that bring several git repos together so a coding harness opened at the package root sees all of them, along with a generated context file that explains why they belong together. It replaces the habit of copying repos out of `~/source/` into a temporary parent folder.

- Members are **git worktrees** of repos already on your machine: no copies, no re-clones, shared object storage.
- The package's purpose and each member's role are written once and rendered into `AGENTS.md` / `CLAUDE.md`.
- A harness opened at the package root works with no changes. Claude Code, OpenCode, Codex and Pi work out of the box; Claude Code and OpenCode also get generated config.
- A bundled skill teaches the harness running inside a package to read and update its context.
- Runs on macOS, Linux and Windows, without admin rights.

## Install

```sh
go install github.com/doneill612/vmono/cmd/vmono@latest
```

Requires git 2.30 or newer.

## Quick start

```sh
vmono                      # opens the TUI; first run walks you through setup
```

Or from the command line:

```sh
vmono init --source-root ~/source
vmono new platform-auth --active api-gateway,sdk-go --ref contracts \
    --purpose-file purpose.md
vmono open platform-auth   # launches your default harness at the package root
cd "$(vmono path platform-auth)" && claude    # same thing, by hand
```

Add `eval "$(vmono shell-init zsh)"` (or `bash`, `fish`, `pwsh`) to your shell rc for a `vcd <pkg>` function.

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
~/.vmono/platform-auth/
├── .vmono/manifest.toml     source of truth
├── AGENTS.md                generated block + your own notes
├── CLAUDE.md                generated: @AGENTS.md
├── .claude/                 generated: settings.json, skills
├── opencode.json            generated
├── api-gateway/             active member (worktree)
└── _ref/contracts/          reference member (detached worktree)
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
fetch_on_create = true
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
