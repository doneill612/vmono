package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/harness"
)

// launchHarness starts a prepared harness. It is a variable so tests can
// observe a launch without replacing the test process.
var launchHarness = harness.Run

// newOpenCmd builds `vmono open`, which launches a harness at a session
// root. Everything after `--` is passed through to the harness.
func newOpenCmd() *cobra.Command {
	var harnessName string
	cmd := &cobra.Command{
		Use:   "open [pkg[/session]] [--harness H] [-- ARGS]",
		Short: "Launch a harness at a session root",
		RunE: func(cmd *cobra.Command, args []string) error {
			pkgArgs, extra := args, []string(nil)
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				pkgArgs, extra = args[:dash], args[dash:]
			}
			if len(pkgArgs) > 1 {
				return errors.New("open takes at most one session; put harness arguments after --")
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			s, err := c.ResolveSession(packageArg(pkgArgs))
			if err != nil {
				return err
			}
			l, err := harness.Prepare(c, s, harnessName, extra)
			if err != nil {
				return err
			}
			return launchHarness(l, true)
		},
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "harness to launch (default: the package's, then the global default)")
	return cmd
}
