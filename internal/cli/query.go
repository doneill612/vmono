package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newLsCmd builds `vmono ls`.
func newLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls [--json]",
		Short: "List packages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			list, err := c.List(true)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if list == nil {
					list = []core.Summary{}
				}
				return printJSON(out, list)
			}
			if len(list) == 0 {
				fprintf(out, "no packages\n")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tMEMBERS\tSESSIONS\tDIRTY\tLAST OPENED")
			for _, s := range list {
				if s.Legacy {
					fmt.Fprintf(tw, "%s\t-\t-\t\tneeds migration: run `vmono doctor --fix`\n", s.Name)
					continue
				}
				dirty := ""
				if s.Dirty {
					dirty = "*"
				}
				opened := s.LastOpened
				if opened == "" {
					opened = "-"
				}
				fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", s.Name, s.Members, s.Sessions, dirty, opened)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newReposCmd builds `vmono repos`.
func newReposCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "repos [--json]",
		Short: "List known repos",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			repos, err := c.Repos()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, repos)
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tBRANCH\tFROM\tPATH")
			for _, r := range repos {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Branch, r.Origin, r.Path)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newStatusCmd builds `vmono status`.
func newStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [pkg[/session]] [--json]",
		Short: "Show each member's branch, uncommitted work and what is left to push in a session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(args))
			if err != nil {
				return err
			}
			st := c.Status(s)
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, st)
			}
			return writeStatusText(out, st)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// writeStatusText prints a session's state as `vmono status` shows it: one
// line naming the session, its branch and its state, then a row per member.
// The session-start hook prints the same text so an agent sees exactly what
// the user would.
func writeStatusText(w io.Writer, st core.SessionStatus) error {
	fprintf(w, "%s/%s  (%s)  %s\n", st.Package, st.Session, st.Branch, st.Label())
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, m := range st.Members {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", m.Name, m.Mode, where(m), cleanliness(m), m.PublishText())
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if hint := progressHint(st.SessionProgress); hint != "" {
		fprintf(w, "%s\n", hint)
	}
	return nil
}

// progressHint says what to do about a session whose work has been merged, or
// probably has, and is empty for any other.
func progressHint(p core.SessionProgress) string {
	switch {
	case p.State == core.SessionMerged:
		return "Everything has been merged; remove the session with `vmono session rm`, and start a new one for more work."
	case len(p.RemoteDeleted) > 0:
		return "The remote branch of " + strings.Join(p.RemoteDeleted, ", ") + " was deleted, probably because its pull request was merged. If it was, remove the session with `vmono session rm`, and start a new one for more work."
	}
	return ""
}

// where describes a member's branch or ref for display.
func where(m core.MemberStatus) string {
	switch {
	case m.Missing:
		return "(missing)"
	case m.Error != "":
		return "(error)"
	case m.Branch != "":
		return m.Branch
	case m.Ref != "":
		return m.Ref + " @" + m.Head
	}
	return "@" + m.Head
}

// cleanliness summarizes dirty and untracked counts.
func cleanliness(m core.MemberStatus) string {
	if m.Missing || m.Error != "" {
		return m.Error
	}
	var parts []string
	if m.Dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", m.Dirty))
	}
	if m.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", m.Untracked))
	}
	if len(parts) == 0 {
		return "clean"
	}
	return strings.Join(parts, ", ")
}

// newPathCmd builds `vmono path`. "pkg/session" prints a session directory,
// "pkg" prints the package directory, and with no argument it is the session
// the working directory is in, or else its package.
func newPathCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "path [pkg[/session]] [--json]",
		Short: "Print a session or package path, for cd \"$(vmono path pkg/session)\"",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, s, err := c.ResolveContext(packageArg(args))
			if err != nil {
				return err
			}
			dir := p.Dir
			info := map[string]string{"name": p.Name}
			if s != nil {
				dir = s.Dir
				info["session"] = s.Name
			}
			info["path"] = dir
			if asJSON {
				return printJSON(cmd.OutOrStdout(), info)
			}
			fprintf(cmd.OutOrStdout(), "%s\n", dir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newDeleteCmd builds `vmono delete`.
func newDeleteCmd() *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "delete <pkg> [--force] [--delete-branches]",
		Short: "Tear down a package and every one of its sessions",
		Long:  "Remove a package and every one of its sessions.\n\n" + branchesHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, err := c.Load(args[0])
			if err != nil {
				return err
			}
			var res core.DeleteResult
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var derr error
				res, derr = c.Delete(p, core.DeleteOptions{Force: force, DeleteBranches: deleteBranches})
				return derr
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fprintf(out, "deleted %s\n", p.Name)
			printBranches(out, res)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete even if members have unsaved work in any session")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false, "also delete the branches whose work has been merged")
	return cmd
}
