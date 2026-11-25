package render

// SkillName is the directory name of the bundled skill.
const SkillName = "vmono"

// skillBody is the bundled skill's SKILL.md. It teaches a harness running
// inside a session to read and refine its package through the vmono CLI.
// It is identical for every session; nothing in it is specific to one, so
// the agent reads the live state with `vmono show --json`.
//
// It is assembled from named sections so that a harness integration with
// more than one entry point, such as the Claude Code plugin's commands, can
// quote the guidance it shares with the skill instead of restating it.
const skillBody = skillHeader + skillIntro + whatGoesWhere + addMemberSection + statusSection + pushSection + updateSection + mergedSection + skillRules

// skillHeader is the frontmatter and generated-file note that open the
// bundled skill.
const skillHeader = `---
name: vmono
description: Use when working inside a vmono session (the VMONO_PACKAGE and VMONO_SESSION environment variables are set, or .vmono/session.toml exists above the working directory). Read the package's purpose, context, notes and members, and keep the package up to date with the vmono CLI when you learn something every future session needs.
---
<!-- ` + GeneratedNote + ` -->

`

// skillIntro explains what a package and a session are, how to recognize
// that the agent is inside one, and to read the live state before writing.
const skillIntro = `# Working inside a vmono session

A vmono **package** groups several git repositories (members) and records why:
a purpose, free-form context, a dated log of notes, and a one-line role per
member. A **session** is one piece of work on a package. Each session has its
own git branches and its own worktree of every member, so several sessions of
the same package can run side by side without touching each other. You are in
one session, and the files around you are that session's worktrees.

The purpose, context, notes and roles belong to the package, not to your
session. Every session of the package, now and later, reads the same text.

## Recognize a session

You are in a session when ` + "`VMONO_PACKAGE`" + ` and ` + "`VMONO_SESSION`" + ` are set, or
` + "`.vmono/session.toml`" + ` exists in the working directory or any parent. Inside a session
the package argument is optional on every command below.

## Read before you write

Run ` + "`vmono show --json`" + ` to get the purpose, context, notes, the other sessions of the
package, and each member's mode, role, branch and git status in your session.
Use that rather than parsing files by hand. Do not edit
` + "`.vmono/session.toml`" + `, ` + "`AGENTS.md`" + ` between the vmono markers, or other generated files
directly; they are rebuilt.

`

// whatGoesWhere says what belongs in the package and what stays in the
// conversation, and lists the commands that write to the package.
const whatGoesWhere = `## What goes where

What this session is trying to do, its progress, and its decisions stay in the
conversation with the user. Do not record them in vmono.

Write to the package only when you learn something that every future session
of it will need: a constraint of the system, a decision that outlives this
task, a link or ticket that frames the whole effort, a change in what a member
is for.

When the user has not asked you to record something, show them the text first
and write it only once they agree. If nothing qualifies, record nothing rather
than inventing a note. Once it is written, tell the user, and say that it
applies to every session.

| Command | Does |
| --- | --- |
| ` + "`vmono note \"TEXT\"`" + ` | Append a dated note to the package. The default choice. |
| ` + "`vmono describe --context \"TEXT\"`" + ` | Replace the context. Run ` + "`vmono show --json`" + ` first and keep what is still true. |
| ` + "`vmono describe --purpose \"TEXT\"`" + ` | Replace the purpose. Only when the user changes what the package is for. |
| ` + "`vmono role MEMBER \"TEXT\"`" + ` | Set a member's one-line role. |

Prefer ` + "`note`" + ` over rewriting the context: it never loses information. For longer
text use ` + "`--context-file FILE`" + ` or ` + "`--purpose-file FILE`" + `.

`

// addMemberSection says when and how to add a repository to the package.
const addMemberSection = `## Add a member

If the work needs a repository the package does not have, ask the user first,
then run ` + "`vmono add REPO --role \"TEXT\"`" + ` (a known repo name or a path to any git repo).
Add ` + "`--ref`" + ` for a read-only reference member. The member is added to every
session of the package, and its worktree in your session exists on disk
immediately. If ` + "`vmono show --json`" + ` has an ` + "`init`" + `, its overview does not cover the
new member yet: suggest that the user run ` + "`vmono package init`" + ` from their own
terminal. Never run it yourself.

`

// statusSection says how to find out how far the work is from pushed and
// merged. The Claude Code plugin's status command quotes it.
const statusSection = `## Check where the session stands

` + "`vmono status --json`" + ` reports, for each member, its branch, any uncommitted or
untracked files, how many commits it has of its own (` + "`own_commits`" + `) on top of
` + "`built_on`" + `, and how far it is from published in ` + "`publish`" + `:

- ` + "`empty`" + `: no commits yet. ` + "`unpublished`" + `: commits, none pushed.
- ` + "`unpushed`" + `, ` + "`pushed`" + `, ` + "`behind`" + `, ` + "`diverged`" + `: compared with ` + "`remote_branch`" + `.
- ` + "`merged`" + `: the work has been merged; ` + "`merged_by`" + ` says whether by a ` + "`merge`" + ` commit, a
  ` + "`rebase`" + ` or a ` + "`squash`" + `.
- ` + "`remote-deleted`" + `: the remote branch is gone, usually because its pull request was
  merged, but the work is not recognizably on the base.

The session's ` + "`state`" + ` is ` + "`in-progress`" + `, ` + "`pushed`" + ` or ` + "`merged`" + `. All of it is as of the
last fetch. Run it when you need to know what is left, and before reviewing or
pushing. Call out uncommitted work, a rebase in progress, a missing worktree, and
members that are merged or ` + "`remote-deleted`" + ` (see After a pull request merges). It
only reads state; it changes nothing.

`

// pushSection is the procedure for wrapping up and pushing a session's
// branches, with the rules that bound it. The Claude Code plugin's push command
// quotes it.
const pushSection = `## Wrapping up and pushing

Each active member is on a branch of its own repository, and that branch is the
result: it is what gets pushed and what a pull request is opened from. There is
nothing to merge back. When the user says the work is done:

1. Commit your work in every member, from inside the member's directory. Only commits
   are pushed.
2. Preview with ` + "`vmono session review --json`" + `. It changes nothing and needs no
   permission.
3. Report, per member: the branch, how many commits it has, and anything uncommitted or
   blocked. Then stop, unless the user asked you to push.

Push only when the user asks for it in this conversation, with
` + "`vmono session push --json`" + `. It asks for permission by design where the harness has
permission rules; where it has none, the user's request is the only permission there
is, so never push on your own judgment. Never use ` + "`git push`" + ` instead, never force,
and never open or merge a pull request unless the user asks for that too.

Read the result. Each member is one of:

- ` + "`pushed`" + `, or ` + "`ready`" + ` in a review: ` + "`branch`" + ` goes to ` + "`remote`" + `/` + "`dest`" + `; ` + "`new`" + ` means the
  push creates it there.
- ` + "`nothing`" + `: no commits, or the remote already has them.
- ` + "`merged`" + `: the work has been merged already. See After a pull request merges.
- ` + "`blocked`" + `: ` + "`reason`" + ` says why. If the branch has diverged from its remote copy, see
  Updating the session. If its remote branch was deleted, see After a pull request
  merges. Do not work around it.
- ` + "`failed`" + `: git refused. Report ` + "`reason`" + `.

A member's ` + "`notes`" + ` do not stop a push: uncommitted files were left out, or the worktree
is on another branch. Tell the user about them.

`

// updateSection is how to bring a session up to date, and what to do when a
// member cannot be. The Claude Code plugin's push command quotes it, since a
// diverged branch is found by pushing.
const updateSection = `## Updating the session

` + "`vmono session update --json`" + ` fetches, then brings each member's branch up to date. A
branch that has not been pushed is rebased onto what it is built on. One that has been
pushed has its remote copy and then its base merged into it, so that pushing it again
never needs force. Run it when the user asks to update, sync with or rebase onto
upstream, or when a push is blocked because the branch diverged. Commit your work first,
and make sure nothing else is working in the session. Each member is one of:

- ` + "`updated`" + ` or ` + "`up-to-date`" + `: after an update, check that the work still builds and passes
  its tests. ` + "`notes`" + ` say when a branch stacked on another moved to the base.
- ` + "`merged`" + `: see After a pull request merges.
- ` + "`skipped`" + `: ` + "`reason`" + ` says why, and the member is unchanged. If it is uncommitted work,
  commit it and run the update again. If history was rewritten, explain that to the user
  and ask; never run ` + "`git reset --hard`" + ` or force-push on your own.
- ` + "`conflict`" + `: it was aborted and the member is unchanged. ` + "`conflicts`" + ` lists the files and
  ` + "`reason`" + ` gives the git commands that reproduce it. Run them in that member's directory
  and resolve keeping both sides' intent. For a rebase, ` + "`git add`" + ` the files and run
  ` + "`git -c core.editor=true rebase --continue`" + `; for a merge, commit it. Then run the update
  again. Ask the user if the right resolution is not clear.
- ` + "`failed`" + `: report ` + "`reason`" + ` to the user.

Never rebase or amend commits that have been pushed. Where a rebase is wanted on a pushed
branch, that is the user's decision and their force-push.

`

// mergedSection is what becomes of a session once its work has been merged, and
// why its branches must not be worked on then. The Claude Code plugin's status
// and push commands quote it.
const mergedSection = `## After a pull request merges

A member whose ` + "`publish`" + ` is ` + "`merged`" + `, or ` + "`remote-deleted`" + `, is usually done with. Its branch
must not be worked on further. After a squash or rebase merge the base holds the same
changes as different commits, so the branch's own commits would conflict with the base
and show up again in a new pull request. ` + "`vmono session update`" + ` and ` + "`vmono session push`" + `
leave such members alone for that reason.

- When everything is merged, tell the user, and that they can remove the session with
  ` + "`vmono session rm PACKAGE/SESSION`" + `, or ctrl+d in the TUI. Say that branches are kept
  unless they add ` + "`--delete-branches`" + `, which deletes the merged ones, squash merges
  included. More work belongs in a new session.
- ` + "`remote-deleted`" + ` cannot be told from a branch someone deleted unmerged. Say so, and ask
  the user whether its pull request was merged.
- If you committed to a branch after it was merged, those commits are not on the base.
  Tell the user. To move them onto the base they need a fresh branch, such as in a new
  session, and ` + "`git rebase --onto BASE LAST_MERGED_COMMIT`" + ` there. Offer it; do not do it
  unasked.

Never remove the session, and never delete a branch, locally or on a remote. You are
running inside the session: removing it deletes the directory you work in and closes
your terminal.

`

// skillRules is what the agent must never do, and when a change to the
// package reaches a running conversation.
const skillRules = `## What you must not do

- Never remove a member, delete a session, or delete the package. Point the
  user to the vmono TUI (run ` + "`vmono`" + ` with no arguments) for those. ` + "`vmono rm`" + `,
  ` + "`vmono session rm`" + ` and ` + "`vmono delete`" + ` ask for permission by design, as do
  ` + "`vmono session push`" + ` and ` + "`vmono edit`" + `; do not work around that.
- Never force-push, and never push with ` + "`git push`" + `.
- Never edit anything under ` + "`_ref/`" + `. Reference members are read-only context.
- Never touch another session's directory. Run git commands from inside a
  member's directory of your own session, never from the session root.

## When changes take effect

Each change is written to the package manifest, regenerated into every
session's files, and committed to the package's own git repo under your author
name, so its history records who changed what. This conversation already has
the change; the regenerated ` + "`AGENTS.md`" + ` is read by the next launch of any session.
`

// SkillMarkdown returns the bundled skill's SKILL.md.
func SkillMarkdown() string { return skillBody }

// writeBundledSkill writes the vmono skill alone at destDir/vmono/SKILL.md.
// It is what a harness gets when its adapter has no entry in pluginWriters;
// see writeIntegration.
func writeBundledSkill(w *writer, destDir string) error {
	return w.file(destDir+"/"+SkillName+"/SKILL.md", SkillMarkdown())
}
