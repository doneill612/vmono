package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newDoctorCmd builds `vmono doctor`.
func newDoctorCmd() *cobra.Command {
	var fix, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor [--fix] [--json]",
		Short: "Find drift (missing worktrees, moved sources, stale entries, orphans), migrate pre-session packages, and rebuild the index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			res, err := c.Doctor(fix)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if err := printJSON(out, res); err != nil {
					return err
				}
			} else {
				for _, f := range res.Findings {
					state := "found"
					if f.Fixed {
						state = "fixed"
					}
					who := f.Package
					if f.Session != "" {
						who += "/" + f.Session
					}
					if f.Member != "" {
						who += "/" + f.Member
					}
					if who != "" {
						who += ": "
					}
					fprintf(out, "[%s] %-16s %s%s\n", state, f.Kind, who, f.Message)
				}
				if len(res.Findings) == 0 {
					fprintf(out, "%d package(s) checked, no problems\n", res.Packages)
				}
			}
			if n := res.Remaining(); n > 0 {
				hint := ""
				if !fix {
					hint = "; run `vmono doctor --fix` to repair what can be repaired"
				}
				return fmt.Errorf("%d problem(s) remain%s", n, hint)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "repair what is safe to repair")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// newInspectCmd builds `vmono inspect`. Its output is always JSON; the
// --json flag is accepted for consistency with the other read commands.
func newInspectCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "inspect [pkg[/session]] --json",
		Short: "Print a session's full resolved model: paths, git common dirs, branches, harness command",
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
			res, err := c.Inspect(s)
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", true, "output JSON (always on)")
	return cmd
}
