package render

// SkillName is the directory name of the bundled skill.
const SkillName = "vmono"

// skillBody is the bundled skill's SKILL.md. It teaches a harness running
// inside a session to read and refine its package through the vmono CLI.
// It is identical for every session; nothing in it is specific to one, so
// the agent reads the live state with `vmono show --json`.
const skillBody = `---
name: vmono
description: Use when working inside a vmono session (the VMONO_PACKAGE and VMONO_SESSION environment variables are set, or .vmono/session.toml exists above the working directory). Read the package's purpose, context, notes and members, and keep the package up to date with the vmono CLI when you learn something every future session needs.
---
<!-- ` + GeneratedNote + ` -->

# Working inside a vmono session

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

## What goes where

What this session is trying to do, its progress, and its decisions stay in the
conversation with the user. Do not record them in vmono.

Write to the package only when you learn something that every future session
of it will need: a constraint of the system, a decision that outlives this
task, a link or ticket that frames the whole effort, a change in what a member
is for. Tell the user when you do, and say that it applies to every session.

| Command | Does |
| --- | --- |
| ` + "`vmono note \"TEXT\"`" + ` | Append a dated note to the package. The default choice. |
| ` + "`vmono describe --context \"TEXT\"`" + ` | Replace the context. Run ` + "`vmono show --json`" + ` first and keep what is still true. |
| ` + "`vmono describe --purpose \"TEXT\"`" + ` | Replace the purpose. Only when the user changes what the package is for. |
| ` + "`vmono role MEMBER \"TEXT\"`" + ` | Set a member's one-line role. |

Prefer ` + "`note`" + ` over rewriting the context: it never loses information. For longer
text use ` + "`--context-file FILE`" + ` or ` + "`--purpose-file FILE`" + `.

## Add a member

If the work needs a repository the package does not have, ask the user first,
then run ` + "`vmono add REPO --role \"TEXT\"`" + ` (a known repo name or a path to any git repo).
Add ` + "`--ref`" + ` for a read-only reference member. The member is added to every
session of the package, and its worktree in your session exists on disk
immediately.

## What you must not do

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

// writeBundledSkill writes the vmono skill at destDir/vmono/SKILL.md.
func writeBundledSkill(w *writer, destDir string) error {
	return w.file(destDir+"/"+SkillName+"/SKILL.md", SkillMarkdown())
}
