package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/launcher"
)

// launchHarness starts a prepared harness in the current terminal, for the
// direct launcher. It is a variable so tests can observe a launch without
// replacing the test process.
var launchHarness = harness.Run

// newOpenCmd builds `vmono open`, which launches a harness at a session
// root. Everything after `--` is passed through to the harness.
//
// Where the harness runs is up to the configured launcher (see package
// launcher): in this terminal, replacing vmono, by default; or in a tmux
// session or herdr workspace of its own, which `open` creates or goes to.
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
			_, err = launcher.Launch(c.Cfg, l, launcher.Opts{Replace: true, RunDirect: launchHarness})
			return err
		},
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "harness to launch (default: the package's, then the global default)")
	return cmd
}
