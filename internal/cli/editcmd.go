package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// runEditor opens a file in the user's editor and waits. It is a variable
// so tests can stand in for a human.
var runEditor = func(file string) error {
	editor := firstNonEmpty(os.Getenv("VISUAL"), os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], file)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// newEditCmd builds `vmono edit`: open the manifest in $EDITOR, then apply
// the changes as a diff.
func newEditCmd() *cobra.Command {
	var force, reuse bool
	cmd := &cobra.Command{
		Use:   "edit [pkg] [--force] [--reuse-branch]",
		Short: "Open the package manifest in $EDITOR, then apply it to every session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			dir, err := os.MkdirTemp("", "vmono-edit-")
			if err != nil {
				return err
			}
			file := filepath.Join(dir, "manifest.toml")
			if err := os.WriteFile(file, manifest.Encode(p.M), 0o600); err != nil {
				return err
			}
			keep := func(cause error) error {
				return fmt.Errorf("%w\nyour edits are kept at %s", cause, file)
			}
			if err := runEditor(file); err != nil {
				return keep(fmt.Errorf("editor: %w", err))
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return keep(err)
			}
			edited, err := manifest.Parse(data)
			if err != nil {
				return keep(err)
			}
			var plan manifest.Plan
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var aerr error
				plan, aerr = c.ApplyEdit(p, edited, core.EditOptions{Force: force, ReuseBranch: reuse})
				return aerr
			})
			if err != nil {
				return keep(err)
			}
			_ = os.RemoveAll(dir)
			out := cmd.OutOrStdout()
			if plan.Empty() {
				fprintf(out, "no changes\n")
				return nil
			}
			fprintf(out, "applied: %d added, %d removed, %d switched, %d updated\n",
				len(plan.Add), len(plan.Remove), len(plan.Switch), len(plan.Update))
			hintInit(out, p)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "allow removals that discard unsaved work")
	cmd.Flags().BoolVar(&reuse, "reuse-branch", false, "check out each session branch if it already exists")
	return cmd
}
