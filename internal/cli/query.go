package cli

import (
	"fmt"
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
			fmt.Fprintln(tw, "NAME\tMEMBERS\tBRANCH\tDIRTY\tLAST OPENED")
			for _, s := range list {
				dirty := ""
				if s.Dirty {
					dirty = "*"
				}
				opened := s.LastOpened
				if opened == "" {
					opened = "-"
				}
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", s.Name, s.Members, s.Branch, dirty, opened)
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
		Use:   "status [pkg] [--json]",
		Short: "Show each member's branch, dirty state and ahead/behind",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, err := c.ResolvePackage(packageArg(args))
			if err != nil {
				return err
			}
			st := c.Status(p)
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, st)
			}
			fprintf(out, "%s  (%s)\n", st.Name, st.Branch)
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			for _, m := range st.Members {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", m.Name, m.Mode, where(m), cleanliness(m), aheadBehind(m))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
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

// aheadBehind formats upstream divergence, empty when there is no upstream.
func aheadBehind(m core.MemberStatus) string {
	if m.Upstream == "" {
		return ""
	}
	return fmt.Sprintf("↑%d ↓%d", m.Ahead, m.Behind)
}

// newPathCmd builds `vmono path`.
func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <pkg>",
		Short: "Print the package path, for cd \"$(vmono path x)\"",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, err := c.ResolvePackage(packageArg(args))
			if err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "%s\n", p.Dir)
			return nil
		},
	}
}

// newDeleteCmd builds `vmono delete`.
func newDeleteCmd() *cobra.Command {
	var force, deleteBranches bool
	cmd := &cobra.Command{
		Use:   "delete <pkg> [--force] [--delete-branches]",
		Short: "Tear down a package",
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
			for _, b := range res.BranchesKept {
				fprintf(out, "kept branch %s\n", b)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "delete even if members have unsaved work")
	cmd.Flags().BoolVar(&deleteBranches, "delete-branches", false, "also delete package branches already merged into their base")
	return cmd
}
