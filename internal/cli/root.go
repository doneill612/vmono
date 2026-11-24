// Package cli implements the vmono command-line interface on top of the core
// operations shared with the TUI.
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/doneill612/vmono/internal/buildinfo"
	"github.com/spf13/cobra"
)

// NewRoot builds the root Cobra command with every subcommand attached.
// runTUI is invoked when vmono is run with no arguments.
func NewRoot(runTUI func() error) *cobra.Command {
	root := &cobra.Command{
		Use:           "vmono",
		Short:         "Manage packages of repos, and the sessions that work on them",
		Version:       buildinfo.Version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runTUI == nil {
				return cmd.Help()
			}
			return runTUI()
		},
	}
	root.PersistentFlags().BoolVar(&noHooks, "no-hooks", false, "skip setup, teardown and the hooks in config.toml")
	root.SetVersionTemplate("{{vmonoVersion .}}\n")
	root.AddCommand(
		newInitCmd(),
		newNewCmd(),
		newLsCmd(),
		newReposCmd(),
		newStatusCmd(),
		newPathCmd(),
		newDeleteCmd(),
		newOpenCmd(),
		newSessionCmd(),
		newShowCmd(),
		newDescribeCmd(),
		newRoleCmd(),
		newNoteCmd(),
		newAddCmd(),
		newRmCmd(),
		newShellInitCmd(),
		newIntegrationCmd(),
		newEditCmd(),
		newSyncCmd(),
		newDoctorCmd(),
		newInspectCmd(),
		newHookCmd(),
	)
	return root
}

// printJSON writes v as indented JSON followed by a newline.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// fprintf is a small helper that ignores write errors to stdout/stderr.
func fprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
