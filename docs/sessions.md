# Working with sessions

[← vmono](../README.md)

A session is one piece of work on a [package](concepts.md). Every active member gets a worktree on a branch, and that branch is the result: it is what you push, and what you open a pull request from. This page covers a session's life: `new`, work, `push`, `rm`, with `update` whenever you need it.

- [Starting a session](#starting-a-session)
- [Pushing](#pushing)
- [Updating](#updating)
- [After your pull request merges](#after-your-pull-request-merges)
- [Removing a session](#removing-a-session)
- [Sessions and packages from earlier versions](#sessions-and-packages-from-earlier-versions)

## Starting a session

`vmono session new [pkg] <name>`, or `n` in the [TUI](tui.md). Every active member is on one branch, named after the session. A new branch starts from the member's base (`origin/main`, usually), whatever its source repo has checked out, and tracks nothing until it is pushed.

| To | Pass |
| --- | --- |
| Name the branch | the session's name: `token-refresh` is on `token-refresh`. Names take letters, digits, hyphens and underscores |
| Put a prefix in front | `--prefix feat`: `feat/token-refresh` |
| Work on that branch when it is already on a remote, such as a teammate's or an open pull request's | `--track`, or answer yes in the TUI |
| Build on something other than the base | `--from REF` or `--from MEMBER=REF` |
| Read a reference member at something other than the package's ref | `--from MEMBER=REF`, or its `read-only` field in the TUI |
| Take up a local branch that already exists | `--reuse-branch` |

Every new session fetches the sources first, even with `fetch_on_create` off, to learn whether its branch is already on a remote. If it is, nothing is created until you say to work on it: the TUI asks, and the command line needs `--track`. So to continue `fix/PLAT-412`, start the session `PLAT-412` with the prefix `fix`.

Working on it checks the branch out itself, tracking the remote one. A local copy is brought up to it when it is only behind. A member whose remote lacks the branch gets a new one of that name, with a warning, and `--track` fails (exit `3`) when no member's remote has it. A remote branch that differs from the session's only in case is refused: spell the name and prefix like it. A branch that is checked out elsewhere, your source repo included, cannot be in a session at the same time, so switch the source to another branch first.

`--from` is for work stacked on a branch that has not merged. `--from REF` is for every active member that has it, and the rest start from their base; `--from MEMBER=REF` is for one. In the TUI, `build on` picks a remote branch for every member, and the row under it for each member picks another, or its base. It cannot be the session's own branch, and does not apply to a member that works on the branch from its remote. The session remembers it, measures your commits against it, and follows it on `update`.

For a reference member, `--from MEMBER=REF` is what it is checked out at in this session: a branch (local or remote, whichever is further ahead), a tag or a commit. Use it when your work depends on changes there that have not merged. `--from REF`, for every member, leaves reference members at the package's ref. The session remembers the ref, and `vmono sync --refs` moves a clean reference member to its newest commit. If the branch's remote copy is deleted, `sync --refs` returns the member to the package's ref once that has the branch's work, and leaves it where it is until then.

A name is also refused if its branch is the one the work merges into, or if it already exists locally (without `--reuse-branch`; the TUI asks). Two packages that share a repo cannot have a session branch of the same name checked out at once; a prefix keeps them apart.

## Pushing

`vmono session push [pkg/session] [--member NAME]... [--dry-run] [--json]`, or `P` on the session (or `p` on a member) in the TUI. `vmono session review` prints the same plan without pushing. Run it as often as you like; commits made since go out with the next push.

Each member's branch goes to the remote branch it tracks or, the first time, to a branch of the same name on `origin` (or on the repo's only remote), which it then tracks. A repo with several remotes and no `origin` blocks that member until you give the branch an upstream with `git branch --set-upstream-to`.

| Member | What push does |
| --- | --- |
| No commits of its own, or nothing the remote lacks | Skips it |
| Uncommitted files | Pushes what is committed, and says what was left out |
| Already merged | Skips it and says how it was merged |
| Remote branch deleted | Blocks it: the pull request was probably merged, and pushing would bring the branch back |
| Diverged from its remote branch | Blocks it: run `vmono session update` |
| A rebase in progress | Blocks it |

Nothing is forced, and a branch is never pushed to the branch it is meant to merge into. Members are independent: one that is blocked or refused does not stop the others, and the command exits `2` when any is blocked (`1` when git refused one). What the remote has is judged from the last fetch (`f` in the TUI, or `git fetch`), and git has the final say.

## Updating

`vmono session update [pkg/session] [--member NAME]... [--no-fetch] [--dry-run]`, or `u` in the TUI, fetches and takes in what is new. Your local `main` is neither read nor moved.

| Branch | What update does |
| --- | --- |
| Not pushed | Rebases your commits onto the base. Nobody else has them |
| Pushed | Merges its remote copy and then the base, fast-forwarding where it can, so pushing again never needs force |
| Built on another branch with `--from` | Follows that branch; once it has merged, moves to the base, leaving its commits behind |
| Already merged | Leaves it alone |
| Remote branch deleted | Skips it |
| Its history, or the remote copy's, was rewritten | Skips it: merging would bring the old commits back |

Members are independent. One with uncommitted changes is skipped when there is something to take in. A rebase or merge that conflicts is aborted, so that member stays exactly as it was, and the reason names the files and the commands to resolve it. The command exits `2` when any member was skipped or conflicted.

An update can rewrite or add to a branch, so do it while no agent is in the middle of a change. If you never run it, conflicts with the base just show up on the pull request.

## After your pull request merges

Once fetched, `vmono status`, `session ls` and the TUI say how each branch merged, and the session's state becomes `merged (merge)`, `merged (rebase)` or `merged (squash)`. Merged sessions are dimmed and sorted last. Then remove the session, and start a new one for more work.

**Do not keep working on a merged branch, above all after a squash merge.** What the merge puts on the base decides why:

| Merge | On the base | Your branch afterwards |
| --- | --- | --- |
| Merge commit | Your commits themselves | Nothing left on it |
| Rebase | Copies of your commits | Its own commits are not on the base, though their changes are |
| Squash | One new commit with all your changes | Same: its own commits are not on the base |

Git cannot tell that a copied or squashed commit is yours. So after a squash or rebase merge, updating the branch can conflict with your own work, and a new pull request from it lists the old commits again. vmono compares the changes instead: `update` and `push` leave a merged branch alone, and tell you to start a new session.

Some merges cannot be recognized: a squash whose changes were edited on the way in, or that resolved a conflict, no longer matches the branch. If the remote branch was deleted, as most hosts can do on merge, the session says `remote branch deleted`, which usually means the same thing, and `update` and `push` stop for it too. If it was not deleted, the branch looks like any pushed branch, and only you know that it merged.

### If you committed after the merge

Commits made after the merge are not on the base. Move them to a fresh branch:

```sh
vmono session new follow-up                   # a new session, from the current base
cd "$(vmono path pkg/follow-up)/MEMBER"
git log --oneline OLD_BRANCH                  # find the last commit that was merged
git cherry-pick LAST_MERGED_COMMIT..OLD_BRANCH
```

On the old branch itself, `git rebase --onto origin/main LAST_MERGED_COMMIT` does the same, and needs a force-push if the branch is still on the remote.

## Removing a session

`vmono session rm pkg/session`, or `ctrl+d` in the TUI, removes the worktrees, the session directory, and the tmux session or herdr workspace it runs in, even when that is where you are (the TUI says so before you confirm). It is refused while a member has uncommitted or untracked files, or commits on no branch (a detached HEAD), unless forced.

Commits on a branch never stop it, since the branch stays in the source repo:

| Branch | On removal |
| --- | --- |
| vmono made it, nothing was committed to it, and it is on no remote | Deleted |
| Merged, by any of the three kinds above | Kept, or deleted with `--delete-branches` (`b` in the TUI) |
| Anything else, with or without `--force` | Kept |

Each branch that is kept or deleted is listed with the reason, before you confirm in the TUI and afterwards on the command line. Sessions are disposable: start one of the same name and prefix with `--track` (or `--reuse-branch` if it was never pushed) to take its branch up again, for example for review feedback.

There is no command that merges into a local branch. To do that yourself, update the session and run `git merge --ff-only BRANCH` in the source repo.

## What the agent does

The vmono skill teaches the agent to commit in every member, preview with `vmono session review`, and report. It runs `vmono session push` only when you ask (in Claude Code, `/vmono:push` asks for you), and Claude Code and OpenCode still ask for permission. It never pushes with `git push`, forces, rebases commits that were pushed, deletes a branch, or removes a session; it tells you when a session is merged and how to remove it. See [What your harness enforces](safety.md#what-your-harness-enforces).

## Sessions and packages from earlier versions

Earlier versions kept a session's work on a private branch and landed it on another with `vmono session finish`. Such a session works as it is, and pushes its own branch under the name it has. `vmono doctor --fix` converts it: the file is rewritten, an upstream of `origin/main` that git gave the branch is dropped, and any landing branch with work that is on no remote is listed, since vmono no longer pushes those. A `post_finish` hook is ignored with a notice; use `post_push`. The `branch_prefix` setting is no longer read; pass `--prefix` when starting a session.

Versions before that kept a package's branch and worktrees in the package itself. Such a package lists as needing migration, and `vmono doctor --fix` converts it into a package with one session named `main` (or `main-2` and so on if a member is already called `main`). The members' worktrees move into the session and are repaired in the source repos, your own text in `AGENTS.md` comes along, the session keeps the old branch, and the package's git history is kept. Harness session history keyed by the old path, which Claude Code uses, does not follow the move.
