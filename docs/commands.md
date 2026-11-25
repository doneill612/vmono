# Command reference

[← vmono](../README.md)

Every read command takes `--json`. Anything that could discard work refuses unless given `--force`. Inside a session, the package and session arguments are optional. Commands that act on a session take `pkg/session`; a bare `pkg` works when it has exactly one session, and otherwise the error lists them.

| Command | Does |
| --- | --- |
| `vmono` | Open the [TUI](tui.md) |
| `vmono init [--source-root P]... [--package-root P]` | Write the global config without prompts |
| `vmono ls` / `vmono repos` | List packages / known repos |
| `vmono new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--harness H] [--session S [--prefix P] [--branch [MEMBER=]B]... [--track [MEMBER=]B]... [--from [MEMBER=]REF]... [--reuse-branch]]` | Define a package, and optionally start its first session |
| `vmono session new [pkg] <name> [--prefix P] [--branch [MEMBER=]B]... [--track [MEMBER=]B]... [--from [MEMBER=]REF]... [--reuse-branch]` | Start a session: a worktree of every member, each active one on a branch. See [Starting a session](sessions.md#starting-a-session) |
| `vmono session ls [pkg]` | List a package's sessions and whether each is in progress, pushed or merged |
| `vmono session update [pkg/session] [--member NAME]... [--no-fetch] [--dry-run] [--json]` | Fetch, then rebase each branch that is not pushed onto its base, and merge what is new into each that is. See [Updating](sessions.md#updating) |
| `vmono session review [pkg/session] [--member NAME]... [--json]` | Show what a push would publish, changing nothing (the read-only form of `session push`) |
| `vmono session push [pkg/session] [--member NAME]... [--dry-run] [--json]` | Push each member's branch to its remote, never forced. See [Pushing](sessions.md#pushing) |
| `vmono session rm <pkg>/<session> [--force] [--delete-branches]` | Remove a session; the package and its other sessions stay. Branches are kept unless empty, or merged and asked for. See [Removing a session](sessions.md#removing-a-session) |
| `vmono package init [pkg] [--harness H]` | Explore the members with a harness and record an overview. See [Initializing a package](package-init.md). `--clean [--force]` removes a checkout an earlier run left behind |
| `vmono package record [pkg] (FILE \| -)` | What the agent records with: JSON with `overview`, and optionally `purpose` and `roles` |
| `vmono package suggestions [pkg] [--review \| --accept ID,... --dismiss ID,... \| --json]` | List, review, accept or dismiss the setup and teardown commands an initialization suggested |
| `vmono add [pkg] <repo> [--ref] [--role R]` | Add a member to the package, and so to every session (a known repo name or any path) |
| `vmono rm [pkg] <member> [--force] [--delete-branch]` | Remove a member from every session |
| `vmono edit [pkg]` | Edit the package manifest in `$EDITOR`; changes apply as a diff to every session |
| `vmono sync [pkg[/session]] [--refs]` | Recreate missing worktrees and regenerate files, in one session or all of a package's; `--refs` moves reference members to the latest fetched commit of their ref, the session's own where it names one |
| `vmono status [pkg[/session]]` | The session's state, and per member: branch or ref, uncommitted work, and how far it is from pushed and merged |
| `vmono show [pkg[/session]]` | Purpose, context, notes, sessions and members; with a session, each member's live state |
| `vmono describe [pkg] --purpose T \| --context T \| --overview T` | Replace the purpose, context or overview (`--purpose-file`, `--context-file`, `--overview-file` too) |
| `vmono role [pkg] <member> TEXT` | Set a member's role |
| `vmono note [pkg] TEXT` | Append a note to the package, stamped with time and author |
| `vmono open [pkg[/session]] [--harness H] [-- ARGS]` | Launch a harness at a session root |
| `vmono path [pkg[/session]]` | Print a session's path, or the package's when no session is meant |
| `vmono delete <pkg> [--force] [--delete-branches]` | Tear down a package and every one of its sessions |
| `vmono inspect [pkg[/session]]` | A session's full resolved model as JSON, including each member's `git_common_dir` |
| `vmono doctor [--fix]` | Find drift, migrate packages and sessions from earlier versions, and rebuild the index |
| `vmono shell-init zsh\|bash\|fish\|pwsh` | Print a `vcd <pkg>[/session]` function |
| `vmono integration ls [--json]` | List integrations and whether each is installed |
| `vmono integration install <name> [--no-launcher] [--key K]` | Write an integration's files, wire them into the tool, and turn on its launcher. See [Integrations](integrations.md) |
| `vmono integration uninstall <name>` | Remove what install added |
| `vmono integration print <name>` | Print an integration's files instead of installing them |

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Error |
| `2` | Refused because work would be lost (or, for `session push`, `review` and `update`, because a member is blocked or was skipped) |
| `3` | Not found |

When a harness started with `vmono open` exits, vmono exits with its status.
