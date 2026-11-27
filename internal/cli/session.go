package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newSessionCmd builds `vmono session`, the commands that start, list, update,
// push and remove the sessions of a package. A session is one piece of work on a
// package: its own branch, and its own worktree of every member.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start, list, update, push and remove the sessions of a package",
	}
	cmd.AddCommand(newSessionNewCmd(), newSessionLsCmd(), newSessionUpdateCmd(), newSessionReviewCmd(), newSessionPushCmd(), newSessionRmCmd(),
		newRemovedCmd("finish", "a session's branches are now what you push: preview with `vmono session review`, publish with `vmono session push`, and remove the session with `vmono session rm`"),
		newRemovedCmd("into", "sessions no longer land on other branches: name the session after the branch to work on, and pass `vmono session new --track`"))
	return cmd
}

// newRemovedCmd builds a hidden command that stands in for one that no longer
// exists and says what to use instead. An agent that learned the old commands
// at the start of its conversation would otherwise only see that they are
// unknown.
func newRemovedCmd(name, instead string) *cobra.Command {
	return &cobra.Command{
		Use:                name,
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("`vmono session %s` was removed; %s", name, instead)
		},
	}
}

// newSessionNewCmd builds `vmono session new`.
func newSessionNewCmd() *cobra.Command {
	var flags sessionFlags
	cmd := &cobra.Command{
		Use:   "new [pkg] <name> [--prefix P] [--from [MEMBER=]REF]... [--track] [--reuse-branch]",
		Short: "Start a session: a worktree of every member, each on a branch of its own",
		Long: `Start a session. Every member gets a worktree in it.

Every active member is on one branch, which is what you push and open a pull
request from: the session's name, after PREFIX/ when you pass --prefix. A new
branch starts from the member's base, the branch it will merge into, or from what
--from names, for work stacked on a branch that has not merged. --from REF is for
every active member that has it, and --from MEMBER=REF for one. It cannot name the
session's own branch.

How --from spells a branch says which copy is meant, and which one update then
follows:

  origin/main       the remote's branch, as of the fetch the new session makes
  refs/heads/main   your local branch, whatever the remote has
  main              whichever of the two is further ahead, and yours if each has
                    commits the other lacks

When the branch is already on a member's remote, such as a teammate's or an open
pull request's, nothing is created unless you pass --track to work on it. A member
whose remote lacks it then gets a new branch of that name.

A read-only (reference) member gets no branch. It is checked out at the package's
ref, or at what --from MEMBER=REF names, for work that depends on changes there that
have not merged.

See ` + "`vmono help concepts`" + ` for the terms.`,
		Example: `  vmono session new payments token-refresh
  vmono session new payments PLAT-412 --prefix fix
  vmono session new payments timeout --prefix fix --track
  vmono session new payments retry-ui --from origin/feat/retry --from web=refs/heads/main
  vmono session new payments try-v2 --from contracts=v2`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			opts, err := flags.options()
			if err != nil {
				return err
			}
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			var s *core.Session
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var cerr error
				s, cerr = c.CreateSession(p, rest[0], opts)
				return cerr
			})
			if err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "%s\n", s.Dir)
			return nil
		},
	}
	flags.add(cmd, "")
	return cmd
}

// newSessionLsCmd builds `vmono session ls`.
func newSessionLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls [pkg] [--json]",
		Short: "List a package's sessions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			rows, err := c.ListSessions(p, true)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, rows)
			}
			if len(rows) == 0 {
				fprintf(out, "no sessions; start one with `vmono session new %s NAME`\n", p.Name)
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "SESSION\tBRANCH\tSTATE\tDIRTY\tLAST OPENED")
			for _, r := range rows {
				dirty := ""
				if r.Dirty {
					dirty = "*"
				}
				opened := r.LastOpened
				if opened == "" {
					opened = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Branch, r.Label(), dirty, opened)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSessionUpdateCmd builds `vmono session update`.
func newSessionUpdateCmd() *cobra.Command {
	var members []string
	var noFetch, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "update [pkg/session] [--member NAME]... [--no-fetch] [--dry-run] [--json]",
		Short: "Bring the session's branches up to date with what they are built on",
		Long: `Bring a session up to date. The sources are fetched, then each active member's
branch takes in what is new. One that has not been pushed is rebased onto its base.
One that has been pushed has its remote copy and then its base merged into it, so
that pushing it again never needs force. A branch built on another with --from moves
to the member's base once that other has merged.

Members are independent. One with uncommitted work is skipped, and so is one whose
remote branch was deleted, as it is once a pull request is merged, or whose history
was rewritten. One whose work has been merged is left as it is: remove the session
and start a new one for more work, since a squash merge leaves the branch's commits
off the base and they would conflict with it. One that conflicts is put back as it
was. The command exits with 2 when any member was skipped or conflicted. A failed
fetch only warns. --dry-run fetches, then shows what would be updated.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			var res core.UpdateResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var uerr error
				res, uerr = c.Update(s, core.UpdateOptions{Members: members, NoFetch: noFetch, DryRun: dryRun})
				return uerr
			})
			out := cmd.OutOrStdout()
			if len(res.Members) > 0 || err == nil {
				if asJSON {
					if perr := printJSON(out, res); perr != nil {
						return perr
					}
				} else {
					printUpdate(out, res, dryRun)
				}
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&members, "member", nil, "update only this member (repeatable)")
	f.BoolVar(&noFetch, "no-fetch", false, "update to the refs already on disk")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be updated without moving any branch")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// printUpdate writes one line per member for an update, or a dry run of one.
func printUpdate(w io.Writer, res core.UpdateResult, dryRun bool) {
	for _, m := range res.Members {
		text := m.Status + ": " + m.Reason
		switch m.Status {
		case core.UpdateMerged:
			text = m.Reason
		case core.UpdateCurrent:
			text = "up to date"
		case core.UpdateDone:
			text = fmt.Sprintf("rebased onto %s (%d behind, %d of its own commits replayed)", m.Onto, m.Behind, m.Commits)
			if m.Merge {
				text = fmt.Sprintf("merged %s (%d commits brought in)", m.Onto, m.Behind)
			}
		case core.UpdateReady:
			text = fmt.Sprintf("would rebase onto %s (%d behind, %d of its own commits to replay)", m.Onto, m.Behind, m.Commits)
			if m.Merge {
				text = fmt.Sprintf("would merge %s (%d commits to bring in)", m.Onto, m.Behind)
			}
		}
		fprintf(w, "%-16s %s\n", m.Name, text)
		for _, n := range m.Notes {
			fprintf(w, "%-16s note: %s\n", "", n)
		}
	}
	if dryRun {
		fprintf(w, "dry run: no branch moved\n")
	}
}

// newSessionPushCmd builds `vmono session push`, which publishes the branches a
// session's work is on. It is the only vmono command that pushes, and it never
// forces.
func newSessionPushCmd() *cobra.Command {
	var members []string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "push [pkg/session] [--member NAME]... [--dry-run] [--json]",
		Short: "Push the session's branches to their remotes",
		Long: `Push the branch each active member is on: to the remote branch it already tracks,
or, when it tracks none, to a branch of the same name on origin, which it then
tracks. Members with no commits of their own, or whose remote already has every
one, are skipped, and so are members whose work has been merged. Uncommitted work
is not pushed, and is pointed out.

Nothing is forced: a remote branch that has commits yours lacks is refused, so run
vmono session update first. A branch is never pushed to the branch it is meant to
merge into, and one whose remote branch was deleted, as it is once a pull request
is merged, is not pushed again. Nothing prompts for credentials, so load your key
into ssh-agent or sign in through your git credential helper beforehand. Whether a
branch is behind is judged from the last fetch; f in the TUI, or git fetch in the
member's source repo, refreshes it.

--member limits the push to the named members (repeatable). Members are
independent: one that cannot be pushed does not stop the others, and the command
exits with 2 when any is blocked, 1 when git refused one. --dry-run shows the plan
and pushes nothing.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd, args, members, dryRun, asJSON)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&members, "member", nil, "push only this member (repeatable)")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be pushed without pushing")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSessionReviewCmd builds `vmono session review`, the read-only form of
// `vmono session push`: it plans the push and prints it, and changes nothing.
// It exists as its own command, rather than as `push --dry-run`, so a harness
// permission rule can allow it while `vmono session push` keeps asking: Claude
// Code lets an ask rule beat any allow rule, however specific, so a dry-run
// flag on a command that asks can never be exempted. Output, JSON and exit
// codes are those of `push --dry-run`.
func newSessionReviewCmd() *cobra.Command {
	var members []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "review [pkg/session] [--member NAME]... [--json]",
		Short: "Show what pushing the session would publish, changing nothing",
		Long: `Show what ` + "`vmono session push`" + ` would do, without doing it: the branch of each active
member, where it would go and how many commits it has that the remote lacks, what
is uncommitted and so left out, and why a member is blocked or has nothing to push.
The command exits with 2 when a member is blocked.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd, args, members, true, asJSON)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&members, "member", nil, "show only this member (repeatable)")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// runPush is the body shared by `vmono session push` and `vmono session review`:
// it plans or performs the push and prints the result as text or JSON. What
// stops a member is printed before the error is returned. With dryRun set
// nothing is changed, which is the only way review calls it.
func runPush(cmd *cobra.Command, args, members []string, dryRun, asJSON bool) error {
	c, err := loadCore()
	if err != nil {
		return err
	}
	s, err := c.ResolveSession(packageArg(args))
	if err != nil {
		return err
	}
	var res core.PushResult
	err = withEvents(cmd.ErrOrStderr(), c, func() error {
		var perr error
		res, perr = c.Push(s, core.PushOptions{Members: members, DryRun: dryRun})
		return perr
	})
	out := cmd.OutOrStdout()
	if len(res.Members) > 0 || err == nil {
		if asJSON {
			if perr := printJSON(out, res); perr != nil {
				return perr
			}
		} else {
			printPush(out, res, dryRun)
		}
	}
	return err
}

// printPush writes one line per member for a push, or a dry run of one.
func printPush(w io.Writer, res core.PushResult, dryRun bool) {
	for _, m := range res.Members {
		fprintf(w, "%-16s %s\n", m.Name, describePush(m, dryRun))
		for _, n := range m.Notes {
			fprintf(w, "%-16s note: %s\n", "", n)
		}
	}
	if dryRun {
		fprintf(w, "dry run: nothing pushed\n")
	}
}

// describePush words one member's outcome.
func describePush(m core.MemberPush, dryRun bool) string {
	switch m.Status {
	case core.PushNothing:
		return "nothing to push"
	case core.PushMerged:
		return m.Reason
	case core.PushBlocked:
		return "blocked: " + m.Reason
	case core.PushFailed:
		return "failed: " + m.Reason
	}
	verb := "pushed"
	if dryRun || m.Status == core.PushReady {
		verb = "would push"
	}
	commits := fmt.Sprintf("%d commit", m.Commits)
	if m.Commits != 1 {
		commits += "s"
	}
	text := fmt.Sprintf("%s %s of %s to %s/%s", verb, commits, m.Branch, m.Remote, m.Dest)
	if m.New {
		text += ", creating the branch"
	}
	if m.SetsUpstream {
		text += ", now tracking it"
		if verb == "would push" {
			text = strings.Replace(text, "now tracking", "to track", 1)
		}
	}
	return text
}

// newSessionRmCmd builds `vmono session rm`.
func newSessionRmCmd() *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "rm <pkg>/<session> [--force] [--delete-branches]",
		Short: "Remove a session and its worktrees; the package and other sessions stay",
		Long: `Remove a session: its worktrees, its directory, and the tmux session or herdr
workspace it runs in, along with everything else running there and even when that is
where you are. It is left open only when an agent running there removes the session.

It is refused while a member has uncommitted work, or commits that are on no branch,
unless forced. To work on its branch again, start a session of the same name and
prefix with --track, or with --reuse-branch for one that was never pushed.

` + branchesHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !strings.Contains(args[0], "/") {
				return fmt.Errorf("name the session as PACKAGE/SESSION, got %q", args[0])
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(args[0])
			if err != nil {
				return err
			}
			var res core.DeleteResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var derr error
				res, derr = c.DeleteSession(s, core.DeleteOptions{Force: force, DeleteBranches: deleteBranches})
				return derr
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fprintf(out, "removed session %s\n", s.Label())
			printBranches(out, res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if members have unsaved work")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false, "also delete the branches whose work has been merged")
	return cmd
}
