// Command vmono manages packages: directories that bring several git
// repositories together as worktrees, with generated context for coding
// harnesses.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/doneill612/vmono/internal/cli"
	"github.com/doneill612/vmono/internal/harness"
)

// main runs the root command. With no arguments the TUI opens; any
// subcommand is handled by Cobra.
func main() {
	if err := cli.NewRoot(nil).Execute(); err != nil {
		var he *harness.ExitError
		if !errors.As(err, &he) {
			fmt.Fprintln(os.Stderr, "vmono:", err)
		}
		os.Exit(cli.ExitCode(err))
	}
}
