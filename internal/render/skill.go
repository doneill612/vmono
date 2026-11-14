package render

// SkillName is the directory name of the bundled skill.
const SkillName = "vmono"

// skillBody is the bundled skill's SKILL.md. It teaches a harness running
// inside a package to read and refine the package through the vmono CLI.
// It is identical for every package; nothing in it is package-specific, so
// the agent reads the live state with `vmono show --json`.
const skillBody = `---
name: vmono
description: Use when working inside a vmono package (the VMONO_PACKAGE environment variable is set, or .vmono/manifest.toml exists above the working directory). Read the package's purpose, context, notes and members, and keep them up to date with the vmono CLI.
---
<!-- ` + GeneratedNote + ` -->

# Working inside a vmono package

A vmono package groups several git repositories (members) under one directory so
you can see all of them at once. Each member is a git worktree. The package has
a purpose, free-form context, a dated log of notes, and a one-line role per
member. You can read and update all of it.

## Recognize a package

You are in a package when ` + "`VMONO_PACKAGE`" + ` is set, or ` + "`.vmono/manifest.toml`" + ` exists in the
working directory or any parent. Inside a package the package name argument is
optional on every command below.

## Read before you write

Run ` + "`vmono show --json`" + ` to get the purpose, context, notes, and each member's mode,
role, branch and git status. Use that rather than parsing files by hand. Do not
edit ` + "`.vmono/manifest.toml`" + `, ` + "`AGENTS.md`" + ` between the vmono markers, or other generated
files directly; they are rebuilt from the manifest.

## Keep the context current

Update the package when the user asks, or when you learn something a future
session will need: a decision made, a constraint found, a ticket or link, a
change in what a member is for. Tell the user when you do.

| Command | Does |
| --- | --- |
| ` + "`vmono note \"TEXT\"`" + ` | Append a dated note. The default choice for decisions and findings. |
| ` + "`vmono describe --context \"TEXT\"`" + ` | Replace the context. Run ` + "`vmono show --json`" + ` first and keep what is still true. |
| ` + "`vmono describe --purpose \"TEXT\"`" + ` | Replace the purpose. Only when the user changes what the work is. |
| ` + "`vmono role MEMBER \"TEXT\"`" + ` | Set a member's one-line role. |

Prefer ` + "`note`" + ` over rewriting the context: it never loses information. For longer
text use ` + "`--context-file FILE`" + ` or ` + "`--purpose-file FILE`" + `.

## Add a member

If the work needs a repository the package does not have, ask the user first,
then run ` + "`vmono add REPO --role \"TEXT\"`" + ` (a known repo name or a path to any git repo).
Add ` + "`--ref`" + ` for a read-only reference member. The new worktree exists on disk
immediately and you can use it in this session.

## What you must not do

- Never remove a member or delete the package. Point the user to the vmono TUI
  (run ` + "`vmono`" + ` with no arguments) for those. ` + "`vmono rm`" + ` and ` + "`vmono delete`" + ` ask for
  permission by design; do not work around that.
- Never edit anything under ` + "`_ref/`" + `. Reference members are read-only context.
- Run git commands from inside a member's directory, never from the package root.

## When changes take effect

Each change is written to the manifest, regenerated into the package files, and
committed to the package's own git repo under your author name, so its history
records who changed what. This session already has the change in its context;
the regenerated ` + "`AGENTS.md`" + ` is read by the next session.
`

// SkillMarkdown returns the bundled skill's SKILL.md.
func SkillMarkdown() string { return skillBody }

// writeBundledSkill writes the vmono skill at destDir/vmono/SKILL.md.
func writeBundledSkill(w *writer, destDir string) error {
	return w.file(destDir+"/"+SkillName+"/SKILL.md", SkillMarkdown())
}
