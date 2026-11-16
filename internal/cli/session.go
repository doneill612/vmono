package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newSessionCmd builds `vmono session`, the commands that start, list and
// remove the sessions of a package. A session is one piece of work on a
// package: its own branch, and its own worktree of every member.
func newSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Start, list and remove the sessions of a package",
	}
	cmd.AddCommand(newSessionNewCmd(), newSessionLsCmd(), newSessionRmCmd())
	return cmd
}

// newSessionNewCmd builds `vmono session new`.
func newSessionNewCmd() *cobra.Command {
	var branch string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new [pkg] <name> [--branch B] [--reuse-branch]",
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
				s, cerr = c.CreateSession(p, rest[0], core.SessionOptions{Branch: branch, ReuseBranch: reuse})
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
			fmt.Fprintln(tw, "SESSION\tBRANCH\tDIRTY\tLAST OPENED")
			for _, r := range rows {
				dirty := ""
				if r.Dirty {
					dirty = "*"
				}
				opened := r.LastOpened
				if opened == "" {
					opened = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Branch, dirty, opened)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
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
