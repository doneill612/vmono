package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// newNewCmd builds `vmono new`, which defines a package from known repo
// names or paths. The package holds no worktrees; with --session it also
// starts its first session.
func newNewCmd() *cobra.Command {
	var active, refs []string
	var purposeFile, contextFile, branch, harness, session string
	var into, track []string
	var reuse bool
	cmd := &cobra.Command{
		Use:   "new <pkg> --active a,b [--ref c] [--purpose-file F] [--context-file F] [--harness H] [--session S [--branch B] [--into B] [--track B]]",
		Short: "Define a package, and optionally start its first session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(active)+len(refs) == 0 {
				return errors.New("a package needs at least one member: pass --active and/or --ref")
			}
			if session == "" && (branch != "" || len(into) > 0 || len(track) > 0 || reuse) {
				return errors.New("--branch, --into, --track and --reuse-branch belong to a session: pass --session NAME, or use `vmono session new`")
			}
			intoAll, intoBy, err := parseBranches("--into", into)
			if err != nil {
				return err
			}
			trackAll, trackBy, err := parseBranches("--track", track)
			if err != nil {
				return err
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
			m := &manifest.Manifest{Name: args[0], Harness: harness, Purpose: purpose, Context: context}
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
			var s *core.Session
			err = withEvents(cmd.ErrOrStderr(), c, func() error {
				var cerr error
				if p, cerr = c.CreatePackage(m); cerr != nil {
					return cerr
				}
				if session != "" {
					s, cerr = c.CreateSession(p, session, core.SessionOptions{Branch: branch, Into: intoAll, IntoByMember: intoBy, Track: trackAll, TrackByMember: trackBy, ReuseBranch: reuse})
				}
				return cerr
			})
			if err != nil {
				if p != nil && session != "" {
					return fmt.Errorf("%w\nthe package %s was created; retry the session with `vmono session new %s %s`", err, p.Name, p.Name, session)
				}
				return err
			}
			if s != nil {
				fprintf(out, "%s\n", s.Dir)
			} else {
				fprintf(out, "%s\n", p.Dir)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&active, "active", nil, "repos to include as active members (names or paths, comma-separated)")
	f.StringSliceVar(&refs, "ref", nil, "repos to include as read-only reference members")
	f.StringVar(&purposeFile, "purpose-file", "", "file holding the package purpose (- for stdin)")
	f.StringVar(&contextFile, "context-file", "", "file holding extra context (- for stdin)")
	f.StringVar(&harness, "harness", "", "default harness for this package")
	f.StringVar(&session, "session", "", "also start a first session with this name")
	f.StringVar(&branch, "branch", "", "with --session: the session branch (default: <branch_prefix><pkg>/<session>)")
	f.StringArrayVar(&into, "into", nil, "with --session: "+intoHelp+"; default is the branch each source has checked out")
	f.StringArrayVar(&track, "track", nil, "with --session: "+trackHelp)
	f.BoolVar(&reuse, "reuse-branch", false, "with --session: check out the branch if it already exists in a source repo")
	return cmd
}
