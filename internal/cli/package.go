package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/pkginit"
)

// runInitHarness runs a harness in the current terminal and returns when it
// exits. It is a variable so tests can stand in for one.
var runInitHarness = harness.RunAndReturn

// newPackageCmd builds `vmono package`, which holds what is done to a package
// as a whole with an agent's help.
func newPackageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Initialize a package's overview with a harness",
	}
	cmd.AddCommand(newPackageInitCmd(), newPackageRecordCmd())
	return cmd
}

// newPackageInitCmd builds `vmono package init`, which opens a harness on a
// temporary, read-only checkout of every member to learn what the package is.
// See package pkginit.
func newPackageInitCmd() *cobra.Command {
	var harnessName string
	var clean, force bool
	cmd := &cobra.Command{
		Use:   "init [pkg] [--harness H] | [pkg] --clean [--force]",
		Short: "Explore the members with a harness and record an overview, purpose and roles",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if force && !clean {
				return errors.New("--force goes with --clean")
			}
			if clean && harnessName != "" {
				return errors.New("--clean starts no harness")
			}
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			if clean {
				w := c.FindInit(p)
				if w == nil {
					fprintf(out, "nothing to clean\n")
					return nil
				}
				if err := withEvents(errOut, c, func() error { return c.CloseInit(w, force) }); err != nil {
					return err
				}
				fprintf(out, "removed %s\n", w.Dir)
				return nil
			}
			res, err := pkginit.Run(c, p, pkginit.Options{
				Harness: harnessName, Replace: true, RunDirect: runInitHarness, Err: errOut,
				Events: func(fn func() error) error { return withEvents(errOut, c, fn) },
			})
			if res.Summary != "" {
				fprintf(out, "%s\n", res.Summary)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&harnessName, "harness", "", "harness to use (default: the package's, then the global default)")
	f.BoolVar(&clean, "clean", false, "remove a temporary checkout an earlier run left behind")
	f.BoolVar(&force, "force", false, "with --clean: discard changes made in it")
	return cmd
}

// newPackageRecordCmd builds `vmono package record`, which the agent started
// by `vmono package init` writes its findings with.
func newPackageRecordCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "record [pkg] (FILE | -) [--json]",
		Short: "Record a package's overview, and its purpose and roles where the user has written none, from JSON",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, rest := splitPkgArgs(args, 2)
			text, err := readTextFlag(cmd, rest[0])
			if err != nil {
				return err
			}
			var rec core.InitRecord
			dec := json.NewDecoder(strings.NewReader(text))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&rec); err != nil {
				return fmt.Errorf("the record is not valid JSON: %w", err)
			}
			c, p, err := resolve(pkg)
			if err != nil {
				return err
			}
			res, err := c.RecordInit(p, rec)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, res)
			}
			fprintf(out, "recorded for %s: %s\n", p.Name, strings.Join(res.Set, ", "))
			if len(res.Kept) > 0 {
				fprintf(out, "kept the user's: %s\n", strings.Join(res.Kept, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
