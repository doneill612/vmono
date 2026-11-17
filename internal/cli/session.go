package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// newSessionCmd builds `vmono session`, the commands that start, list, finish
// and remove the sessions of a package. A session is one piece of work on a
// package: its own branch, and its own worktree of every member.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start, list, finish and remove the sessions of a package",
	}
	cmd.AddCommand(newSessionNewCmd(), newSessionLsCmd(), newSessionIntoCmd(), newSessionFinishCmd(), newSessionRmCmd())
	return cmd
}

// newSessionNewCmd builds `vmono session new`.
func newSessionNewCmd() *cobra.Command {
	var branch string
	var into []string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new [pkg] <name> [--branch B] [--into [MEMBER=]B]... [--reuse-branch]",
		Short: "Start a session: a worktree of every member on a new branch",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			intoAll, intoBy, err := parseInto(into)
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
				s, cerr = c.CreateSession(p, rest[0], core.SessionOptions{Branch: branch, Into: intoAll, IntoByMember: intoBy, ReuseBranch: reuse})
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
	cmd.Flags().StringArrayVar(&into, "into", nil, intoHelp+"; default is the branch each source has checked out. A session starts from its landing branch when that is not the member's base branch and exists")
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

// newSessionIntoCmd builds `vmono session into`: show where each member's work
// will land, or change it.
func newSessionIntoCmd() *cobra.Command {
	var all string
	var members []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "into [pkg/session] [--all B] [--member NAME=B]... [--json]",
		Short: "Show, or change, the branch each member's work lands on",
		Long: `Without flags, list each active member's landing branch in the session.
--all sets one branch for every active member and --member NAME=BRANCH sets one
member's, which wins over --all. Members you do not name keep theirs. A branch that
does not exist yet is created when the session is finished.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			byMember, err := parseMembers(members)
			if err != nil {
				return err
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			if all != "" || len(byMember) > 0 {
				if err := c.SetInto(s, all, byMember); err != nil {
					return err
				}
				if s, err = c.ResolveSession(s.Label()); err != nil {
					return err
				}
			}
			type row struct {
				Member string `json:"member"`
				Into   string `json:"into"`
			}
			var rows []row
			for _, m := range c.Status(s).Members {
				if m.Mode == manifest.ModeActive {
					rows = append(rows, row{m.Name, m.Into})
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, rows)
			}
			for _, r := range rows {
				fprintf(out, "%-16s %s\n", r.Member, r.Into)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&all, "all", "", "land every active member on this branch")
	f.StringArrayVar(&members, "member", nil, "land one member on a branch: NAME=BRANCH (repeatable)")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// parseMembers reads NAME=BRANCH pairs.
func parseMembers(vals []string) (map[string]string, error) {
	_, by, err := parseInto(vals)
	if err != nil {
		return nil, err
	}
	for _, v := range vals {
		if !strings.Contains(v, "=") {
			return nil, fmt.Errorf("--member %q: expected NAME=BRANCH", v)
		}
	}
	return by, nil
}

// newSessionFinishCmd builds `vmono session finish`.
func newSessionFinishCmd() *cobra.Command {
	var into []string
	var closeAfter, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "finish [pkg/session] [--into [MEMBER=]B]... [--close] [--dry-run] [--json]",
		Short: "Land the session's commits on each member's branch in its source repo",
		Long: `Land the session's work. Each active member's own commits are rebased onto its
landing branch inside the session, then the landing branch in the member's source
repo is moved to the result: fast-forwarded when it is checked out in a clean
worktree, otherwise updated in place, and created when it does not exist yet.
Members with no commits of their own are skipped.

Each member lands on its own landing branch: the one recorded when the session was
created (by default the branch its source had checked out), else the branch its
base names (origin/main lands on main). --into overrides and records it. Nothing moves unless every member can land: commit
or stash uncommitted work first. If a rebase stops on conflicts, resolve them in
that member, run git add and git rebase --continue, then run this command again.

The session, its worktrees and its branch stay in place and the session is marked
finished; remove it with ` + "`vmono session rm`" + ` or pass --close. Nothing is pushed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			intoAll, intoBy, err := parseInto(into)
			if err != nil {
				return err
			}
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
				res, ferr = c.Finish(s, core.FinishOptions{Into: intoAll, IntoByMember: intoBy, Close: closeAfter, DryRun: dryRun})
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
	f.StringArrayVar(&into, "into", nil, intoHelp+"; overrides what is recorded, and is recorded")
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
