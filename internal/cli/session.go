package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newSessionCmd builds `vmono session`, the commands that start, list, finish
// and remove the sessions of a package. A session is one piece of work on a
// package: its own branch, and its own worktree of every member.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start, list, finish and remove the sessions of a package",
	}
	cmd.AddCommand(newSessionNewCmd(), newSessionLsCmd(), newSessionFinishCmd(), newSessionRmCmd())
	return cmd
}

// newSessionNewCmd builds `vmono session new`.
func newSessionNewCmd() *cobra.Command {
	var branch, into string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new [pkg] <name> [--branch B] [--into B] [--reuse-branch]",
		Short: "Start a session: a worktree of every member on a new branch",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			var s *core.Session
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var cerr error
				s, cerr = c.CreateSession(p, rest[0], core.SessionOptions{Branch: branch, Into: into, ReuseBranch: reuse})
				return cerr
			})
			if err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "%s\n", s.Dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "session branch (default: <branch_prefix><pkg>/<session>)")
	cmd.Flags().StringVar(&into, "into", "", "the branch the work lands on when the session is finished (default: each member's base branch); the session starts from it when it exists")
	cmd.Flags().BoolVar(&reuse, "reuse-branch", false, "check out the branch if it already exists in a source repo")
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
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, r.Branch, r.State, dirty, opened)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newSessionFinishCmd builds `vmono session finish`.
func newSessionFinishCmd() *cobra.Command {
	var into string
	var closeAfter, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "finish [pkg/session] [--into B] [--close] [--dry-run] [--json]",
		Short: "Land the session's commits on each member's branch in its source repo",
		Long: `Land the session's work. Each active member's own commits are rebased onto its
landing branch inside the session, then the landing branch in the member's source
repo is moved to the result: fast-forwarded when it is checked out in a clean
worktree, otherwise updated in place, and created when it does not exist yet.
Members with no commits of their own are skipped.

The landing branch is the session's --into, else each member's base branch
(origin/main lands on main). Nothing moves unless every member can land: commit
or stash uncommitted work first. If a rebase stops on conflicts, resolve them in
that member, run git add and git rebase --continue, then run this command again.

The session, its worktrees and its branch stay in place and the session is marked
finished; remove it with ` + "`vmono session rm`" + ` or pass --close. Nothing is pushed.`,
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
			var res core.FinishResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var ferr error
				res, ferr = c.Finish(s, core.FinishOptions{Into: into, Close: closeAfter, DryRun: dryRun})
				return ferr
			})
			out := cmd.OutOrStdout()
			switch {
			case asJSON && (err == nil || errors.Is(err, core.ErrRefused)):
				if perr := printJSON(out, res); perr != nil {
					return perr
				}
			case err == nil || errors.Is(err, core.ErrRefused):
				printFinish(out, res, dryRun)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&into, "into", "", "land on this branch instead of the session's own or each member's base branch (recorded in the session)")
	f.BoolVar(&closeAfter, "close", false, "remove the session once its work has landed")
	f.BoolVar(&dryRun, "dry-run", false, "show what would land without changing anything")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// printFinish writes one line per member for a finish, or a dry run of one,
// then a summary line. Refused finishes print the same lines with the reason
// each member is stopped.
func printFinish(w io.Writer, res core.FinishResult, dryRun bool) {
	label := res.Package + "/" + res.Session
	for _, m := range res.Members {
		fprintf(w, "%-16s %s\n", m.Name, describeLanding(m, dryRun))
		for _, n := range m.Notes {
			fprintf(w, "%-16s note: %s\n", "", n)
		}
	}
	switch {
	case res.Closed:
		fprintf(w, "finished and removed session %s\n", label)
	case res.Finished:
		fprintf(w, "finished session %s; remove it with `vmono session rm %s` when you are done\n", label, label)
	case dryRun:
		fprintf(w, "dry run: nothing changed\n")
	}
}

// describeLanding words one member's outcome.
func describeLanding(m core.MemberLanding, dryRun bool) string {
	commits := fmt.Sprintf("%d commit", m.Commits)
	if m.Commits != 1 {
		commits += "s"
	}
	verb := "landed"
	if dryRun || m.Status == core.LandReady {
		verb = "would land"
	}
	switch m.Status {
	case core.LandNothing:
		return "nothing to land"
	case core.LandBlocked:
		return "blocked: " + m.Reason
	case core.LandConflict:
		return fmt.Sprintf("conflict: rebase onto %s stopped in %s; %s", m.Into, strings.Join(m.Conflicts, ", "), m.Reason)
	}
	text := fmt.Sprintf("%s %s on %s (%s)", verb, commits, m.Into, m.Via)
	if m.Create {
		text += ", creating the branch"
	}
	return text
}

// newSessionRmCmd builds `vmono session rm`.
func newSessionRmCmd() *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "rm <pkg>/<session> [--force] [--delete-branches]",
		Short: "Remove a session and its worktrees; the package and other sessions stay",
		Args:  cobra.ExactArgs(1),
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
			for _, b := range res.BranchesKept {
				fprintf(out, "kept branch %s\n", b)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove even if members have unsaved work")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false, "also delete the session branch if already merged into its base")
	return cmd
}
