# Concepts

[← vmono](../README.md)

| Term | Meaning |
| --- | --- |
| Package | A definition under the package root (default `~/.vmono`): the members, a purpose, context, notes and a role per member. It has its own small git repo, and holds no worktrees. |
| Session | One piece of work on a package: a directory with its own git repo and a worktree of every member, the active ones all on one branch. It is what a harness opens. A package has any number of them side by side. |
| Member | A source repo included in a package. Every session has a worktree of every member. |
| Active member | Checked out on the session's branch. The agent may edit it and commit to it, and you push it. |
| Reference member | Checked out detached under `_ref/`, at the package's `ref` or what a session names with `--from MEMBER=REF`. Read-only context, and shown as `read-only` in the TUI. |
| Base | What a member's branch is built on and merges into: the `base` in the package manifest (default `origin/HEAD`), or what a session names with `--from`. Shown as `built on` in the TUI. |
| Branch | The one branch a session's active members are on: the session's name, after the prefix if there is one. |
| Build on | Start a session's new branches on something other than the base, per member if they differ: `--from`, or `build on` in the TUI. |
| Prefix | What goes before the session's name, with a slash, in its branch: `--prefix`. |
| Published | How far an active member's branch has got: no commits yet, commits to push, pushed, behind, diverged or merged. |
| Overview | What an agent learned by exploring the members, recorded by `vmono package init`. Kept apart from the text you write. |
| Purpose / Context / Notes / Role | Free text that belongs to the package: what the work is, what to know, a dated log, and what each member contributes. Sessions share it; they have none of their own. |

`vmono help concepts` explains these in a terminal, and `?` then `t` in the [TUI](tui.md).

## Layout on disk

A package with two sessions looks like this:

```text
~/.vmono/project/
├── .vmono/manifest.toml     source of truth for the package
├── .git/  .gitignore        the package's own repo: tracks only .vmono/
├── token-refresh/           a session: what the harness opens
│   ├── .vmono/session.toml  name, package, branches
│   ├── .git/                the session's own repo (one harness project per session)
│   ├── AGENTS.md            generated block + your own notes
│   ├── CLAUDE.md            generated: @AGENTS.md
│   ├── .claude/             generated: settings.json, skills/vmono (the Claude Code plugin)
│   ├── .agents/             generated: skills/vmono, for Codex and Pi
│   ├── opencode.json        generated
│   ├── member-a/            active member (worktree on token-refresh)
│   └── _ref/member-c/       reference member (detached worktree)
└── spike/                   another session, with its own branches and worktrees
```

Everything except the manifest, the session file and your own text in `AGENTS.md` (outside the `<!-- vmono:begin -->` / `<!-- vmono:end -->` markers) is rebuilt from the manifest. Changing the package (a note, a new member, an edit) applies to every session. Task details that belong to one piece of work stay in the harness conversation, and the vmono skill tells the agent to write to the package only what every future session will need. Packages and sessions cannot be renamed: harness session history is keyed by path.

## Branches

A session's branch is its name, after a `--prefix` if you give one, and every active member is on it. It is the branch you push, so the name is public. When it is already on a remote, vmono asks before working on it there. A branch can be checked out in only one worktree at a time, and git cannot hold both `feat` and `feat/spike`. If a name in the way already exists locally, vmono says so; it never renames branches.

## Where to next

- [Working with sessions](sessions.md) for how branches are pushed, updated and cleaned up.
- [Setup and teardown](setup-and-teardown.md) for preparing each member's worktree.
- [The Claude Code plugin](claude-code-plugin.md) and [Initializing a package](package-init.md) for the agent side.
