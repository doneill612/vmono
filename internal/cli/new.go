package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// newNewCmd builds `vmono new`, which creates a package from known repo
// names or paths.
func newNewCmd() *cobra.Command {
	var active, refs []string
	var purposeFile, contextFile, branch, harness string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--branch B] [--harness H]",
		Short: "Create a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(active)+len(refs) == 0 {
				return errors.New("a package needs at least one member: pass --active and/or --ref")
			}
			c, err := loadCore()
			if err != nil {
				return err
			}
			if harness != "" {
				if _, ok := c.Cfg.Harness[harness]; !ok {
					return fmt.Errorf("unknown harness %q (configured: %v)", harness, c.Cfg.HarnessNames())
				}
			}
			purpose, err := readTextFlag(cmd, purposeFile)
			if err != nil {
				return err
			}
			context, err := readTextFlag(cmd, contextFile)
			if err != nil {
				return err
			}
			m := &manifest.Manifest{Name: args[0], Branch: branch, Harness: harness, Purpose: purpose, Context: context}
			for _, group := range []struct {
				names []string
				mode  string
			}{{active, manifest.ModeActive}, {refs, manifest.ModeReference}} {
				for _, name := range group.names {
					r, err := c.ResolveRepo(name)
					if err != nil {
						return err
					}
					m.Members = append(m.Members, manifest.Member{Source: r.Path, Mode: group.mode})
				}
			}
			out := cmd.OutOrStdout()
			var p *core.Package
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var cerr error
				p, cerr = c.Create(m, core.CreateOptions{ReuseBranch: reuse})
				return cerr
			})
			if err != nil {
				return err
			}
			fprintf(out, "%s\n", p.Dir)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&active, "active", nil, "repos to include as active members (names or paths, comma-separated)")
	f.StringSliceVar(&refs, "ref", nil, "repos to include as read-only reference members")
	f.StringVar(&purposeFile, "purpose-file", "", "file holding the package purpose (- for stdin)")
	f.StringVar(&contextFile, "context-file", "", "file holding extra context (- for stdin)")
	f.StringVar(&branch, "branch", "", "package branch (default: <branch_prefix><pkg>)")
	f.StringVar(&harness, "harness", "", "default harness for this package")
	f.BoolVar(&reuse, "reuse-branch", false, "check out the branch if it already exists in a source repo")
	return cmd
}
