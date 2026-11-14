// Command vmono manages packages: directories that bring several git
// repositories together as worktrees, with generated context for coding
// harnesses.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// main runs the root command. With no arguments the TUI will open; any
// subcommand is handled by Cobra.
func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "vmono:", err)
		os.Exit(1)
	}
}

// newRootCmd builds the Cobra root command.
func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "vmono",
		Short:         "Manage packages of git worktrees for coding harnesses",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
}
