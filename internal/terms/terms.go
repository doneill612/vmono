// Package terms holds the words vmono uses for its concepts and what each one
// means, so the terminal UI and the command help explain them the same way.
// docs/concepts.md is the long form for readers of the repository.
package terms

import "github.com/doneill612/vmono/internal/manifest"

// Entry is one concept: the word the user sees, a line short enough for the
// help dialog, and a fuller explanation for the command line.
type Entry struct {
	Term  string
	Short string
	Long  string
}

// Entries lists the concepts, each defined before the ones that build on it.
var Entries = []Entry{
	{
		Term:  "package",
		Short: "repos that go together, with notes on them",
		Long: "A set of repos you work on together, with a purpose, context, notes and a " +
			"role for each repo. It holds no code of its own; sessions do.",
	},
	{
		Term:  "session",
		Short: "one piece of work, in a directory of its own",
		Long: "One piece of work on a package: a directory with a worktree of every " +
			"member. It is what a harness opens, and a package can have many side by side.",
	},
	{
		Term:  "member",
		Short: "a repo in a package",
		Long: "A source repo included in a package. Every session has a worktree of " +
			"every member.",
	},
	{
		Term:  "active",
		Short: "a member you change, commit to and push",
		Long: "A member checked out on the session's branch. The agent may edit it and " +
			"commit to it, and you push it.",
	},
	{
		Term:  "read-only",
		Short: "a reference member: there to read, not to change",
		Long: "A reference member, checked out under _ref/ with no branch. It is context " +
			"to read. It is checked out at the package's ref (a branch, tag or commit), " +
			"or at what a session names with --from MEMBER=REF.",
	},
	{
		Term:  "branch",
		Short: "the session's name, which active members are on",
		Long: "The one branch a session's active members are on, and what you push: the " +
			"session's name, after the prefix and a slash if there is one. When it is " +
			"already on a remote, vmono asks before working on it there (--track on the " +
			"command line).",
	},
	{
		Term:  "base",
		Short: "what an active member's branch is built on",
		Long: "The branch an active member's new branch starts from and merges into. It " +
			"is set in the package (default origin/HEAD, the remote's default branch). " +
			"Shown as \"built on\".",
	},
	{
		Term:  "build on",
		Short: "start new branches somewhere other than the base",
		Long: "--from, when starting a session: start new branches on something other " +
			"than the base, for work stacked on a branch that has not merged yet. It can " +
			"differ for each member, and is never the session's own branch. A local " +
			"branch and the remote's are separate choices: origin/main is the remote's. " +
			"On the command line refs/heads/main is yours, and a plain main is whichever " +
			"is further ahead.",
	},
	{
		Term:  "prefix",
		Short: "goes before the session name in its branch",
		Long: "--prefix, when starting a session: with the prefix feat, the session " +
			"retry works on the branch feat/retry.",
	},
	{
		Term:  "published",
		Short: "how much of a branch is on its remote or merged",
		Long: "How far an active member's branch has got: no commits yet, commits to " +
			"push, pushed, behind, diverged or merged. It is as of the last fetch.",
	},
	{
		Term:  "3~ 2?",
		Short: "3 modified files and 2 untracked ones",
		Long: "In narrow views, the uncommitted work in a member: the number before ~ " +
			"counts modified files, the number before ? counts untracked ones.",
	},
}

// ModeLabel is how a member's mode, as the manifest spells it, is shown to the
// user. The short form fits a table column; the long form, for detail views,
// also says that a read-only member is a reference member.
func ModeLabel(mode string, long bool) string {
	switch {
	case mode != manifest.ModeReference:
		return "active"
	case long:
		return "reference (read-only)"
	}
	return "read-only"
}
