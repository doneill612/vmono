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
const skillBody = skillHeader + skillIntro + whatGoesWhere + addMemberSection + statusSection + finishSection + updateSection + skillRules

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
own git branch and its own worktree of every member, so several sessions of
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

// statusSection says how to find out what is left to land, and what would
// stop it from landing. The Claude Code plugin's status command quotes it.
const statusSection = `## Check where the session stands

` + "`vmono status --json`" + ` reports the session's ` + "`state`" + ` (in-progress or finished) and, for
each member, its branch, any uncommitted or untracked files, and how many
commits are left to land on its ` + "`into`" + ` branch. Run it when you need to know what
is left, and before finishing. Call out anything that would block
` + "`vmono session finish`" + `: uncommitted work, a rebase in progress, a missing
worktree. It only reads state; it changes nothing.

`

// finishSection is the procedure for landing a session's commits, with the
// rules that bound it. The Claude Code plugin's finish command quotes it.
const finishSection = `## Finishing the session

Finishing lands the session's commits on a branch in each member's source repo.
Each member has its own landing branch, by default the branch its source repo had
checked out when the session began; ` + "`vmono show --json`" + ` lists it as ` + "`into`" + ` on every
member, and ` + "`vmono session into`" + ` prints them. Do it only when the user says the work
is done or asks to land or merge it back.

1. Commit your work in every member, from inside the member's directory.
   ` + "`vmono session finish`" + ` refuses uncommitted changes and untracked files.
2. Preview with ` + "`vmono session finish --dry-run --json`" + ` and read it as in step 3, then run
   ` + "`vmono session finish --json`" + `. It asks the user for permission by design.
3. Read the result. Each member is one of:
   - ` + "`landed`" + `: its commits are now on the branch in ` + "`into`" + `.
   - ` + "`nothing`" + `: it had no commits of its own.
   - ` + "`blocked`" + `: ` + "`reason`" + ` says why. Fix what is in your session (commit, finish a
     rebase) and run it again. If it names ` + "`vmono session update`" + `, see Updating the
     session below. If the cause is in the source repo, such as
     uncommitted changes where the branch is checked out, tell the user; do not
     touch the source repo yourself.
   - ` + "`conflict`" + `: the rebase onto the landing branch stopped. ` + "`conflicts`" + ` lists the
     files, inside that member's directory. Resolve them keeping both sides'
     intent, run ` + "`git add`" + ` on them, then ` + "`git -c core.editor=true rebase --continue`" + `,
     and run ` + "`vmono session finish`" + ` again. Nothing has moved while any member is
     blocked or conflicted, so it is safe to stop and ask the user.
   A member's ` + "`notes`" + ` do not stop the finish. If the preview notes that a landing
   branch has diverged from its upstream, landing works but the user's push will
   be refused. Tell them, and offer ` + "`vmono session update --merge`" + ` before finishing.
4. Tell the user which branch each member landed on and how many commits.

The session stays in place afterward, marked finished (` + "`vmono show --json`" + ` reports
its ` + "`state`" + `). New commits reopen it, and finishing again lands only those.

- Never pass ` + "`--close`" + `: you are running inside the session. Tell the user they can
  remove it from the vmono TUI or with ` + "`vmono session rm`" + `.
- Never push, and never delete branches. Landing is local; pushing and pull
  requests are the user's.
- Change a landing branch only if the user names one: ` + "`vmono session into --member NAME=BRANCH`" + `
  (or ` + "`--into NAME=BRANCH`" + ` on finish). Never pick a different branch yourself.

`

// updateSection is how to catch a session up with its landing branches, and
// what to do when one has diverged. The Claude Code plugin's finish command
// quotes it, since finishing is where an agent usually needs it.
const updateSection = `## Updating the session

` + "`vmono session update --json`" + ` fetches, then rebases each member's commits onto the
latest of its landing branch. Run it when the user asks to update, sync with or
rebase onto upstream, or when finish tells you to. Commit your work first, and
make sure nothing else is working in the session, since it rewrites the session
branch. Each member is one of:

- ` + "`updated`" + ` or ` + "`up-to-date`" + `: nothing to do. After an update, check that the work
  still builds and passes its tests.
- ` + "`skipped`" + `: ` + "`reason`" + ` says why, and the member is unchanged. If it is uncommitted work,
  commit it and run the update again. If the landing branch and its remote copy
  have diverged, explain that to the user and ask before running
  ` + "`vmono session update --merge --json`" + `. It merges both sides into the session, keeps
  every commit, and lets finish and the user's push go through as fast-forwards,
  but the merge commit lands with the work. Never use ` + "`--merge`" + ` without asking, and
  never reconcile the branches in the source repo yourself.
- ` + "`conflict`" + `: it was aborted and the member is unchanged. ` + "`conflicts`" + ` lists the files and
  ` + "`reason`" + ` gives the git commands that reproduce it. Run them in that member's
  directory and resolve keeping both sides' intent. For a rebase, ` + "`git add`" + ` the files
  and run ` + "`git -c core.editor=true rebase --continue`" + `; for a merge, commit it. Then run
  the update again. Ask the user if the right resolution is not clear.
- ` + "`failed`" + `: report ` + "`reason`" + ` to the user.

` + "`finish`" + ` blocks a member whose landing branch moved after the session took in
upstream's commits, because rebasing would duplicate them. If the user already
agreed to merge that member, or once they do, run ` + "`vmono session update --merge --json`" + ` and finish again.

`

// skillRules is what the agent must never do, and when a change to the
// package reaches a running conversation.
const skillRules = `## What you must not do

- Never remove a member, delete a session, or delete the package. Point the
  user to the vmono TUI (run ` + "`vmono`" + ` with no arguments) for those. ` + "`vmono rm`" + `,
  ` + "`vmono session rm`" + ` and ` + "`vmono delete`" + ` ask for permission by design; do not work
  around that.
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
