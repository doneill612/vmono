package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/herdr"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/pkginit"
	"github.com/doneill612/vmono/internal/tui"
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
	cmd.AddCommand(newPackageInitCmd(), newPackageRecordCmd(), newPackageSuggestionsCmd())
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
			if !res.Direct || errors.Is(err, harness.ErrStopped) {
				return err
			}
			return errors.Join(err, reviewAfterInit(out, c, p.Name, res.Summary))
		},
	}
	f := cmd.Flags()
	f.StringVar(&harnessName, "harness", "", "harness to use (default: the package's, then the global default)")
	f.BoolVar(&clean, "clean", false, "remove a temporary checkout an earlier run left behind")
	f.BoolVar(&force, "force", false, "with --clean: discard changes made in it")
	return cmd
}

// runReview opens the review of a package's suggestions, and interactive says
// whether there is a terminal to show it in. They are variables for tests.
var (
	runReview   = tui.Review
	interactive = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
)

// reviewAfterInit goes from a finished initialization straight into the review
// of what the agent suggested, and says what is left. Without a terminal it
// only says so.
func reviewAfterInit(out io.Writer, c *core.Core, name, notice string) error {
	p, err := c.Load(name)
	if err != nil || len(p.M.Pending()) == 0 {
		return err
	}
	if interactive() {
		if err := runReview(c, name, notice); err != nil {
			return err
		}
		if p, err = c.Load(name); err != nil {
			return err
		}
	}
	if n := len(p.M.Pending()); n > 0 {
		fprintf(out, "%s to review: `vmono package suggestions %s --review`\n", countCommands(n), name)
	}
	return nil
}

// countCommands counts suggested commands.
func countCommands(n int) string {
	if n == 1 {
		return "1 suggested command"
	}
	return fmt.Sprintf("%d suggested commands", n)
}

// suggestionJSON is a suggestion with the id that settles it.
type suggestionJSON struct {
	ID string `json:"id"`
	manifest.Suggestion
}

// newPackageSuggestionsCmd builds `vmono package suggestions`, which lists and
// settles the commands an agent suggested while initializing a package.
func newPackageSuggestionsCmd() *cobra.Command {
	var accept, dismiss []string
	var review, asJSON bool
	cmd := &cobra.Command{
		Use:   "suggestions [pkg] [--review | --accept ID,... --dismiss ID,... | --json]",
		Short: "List, accept or dismiss the setup and teardown commands an agent suggested",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var decisions []core.SuggestionDecision
			for _, id := range accept {
				decisions = append(decisions, core.SuggestionDecision{ID: id, Accept: true})
			}
			for _, id := range dismiss {
				decisions = append(decisions, core.SuggestionDecision{ID: id})
			}
			switch {
			case review && !interactive():
				return errors.New("--review needs a terminal")
			case review && strings.HasPrefix(c.Author, "agent:"):
				return core.ErrAgentReview
			case review:
				return runReview(c, p.Name, "")
			case len(decisions) > 0:
				if err := c.ReviewSuggestions(p, decisions); err != nil {
					return err
				}
				fprintf(out, "accepted %d, dismissed %d\n", len(accept), len(dismiss))
				return nil
			}
			pending := p.M.Pending()
			if asJSON {
				list := []suggestionJSON{}
				for _, s := range pending {
					list = append(list, suggestionJSON{s.ID(), s})
				}
				return printJSON(out, list)
			}
			if len(pending) == 0 {
				fprintf(out, "nothing to review\n")
			}
			for _, s := range pending {
				fprintf(out, "%s  %s  %s\n    $ %s\n", s.ID(), s.Member, s.Kind, s.Command)
				if s.Reason != "" {
					fprintf(out, "      %s\n", s.Reason)
				}
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&review, "review", false, "go through them one by one")
	f.StringSliceVar(&accept, "accept", nil, "ids to add to their member's commands")
	f.StringSliceVar(&dismiss, "dismiss", nil, "ids to turn down for good")
	f.BoolVar(&asJSON, "json", false, "output JSON")
	cmd.MarkFlagsMutuallyExclusive("review", "accept", "json")
	cmd.MarkFlagsMutuallyExclusive("review", "dismiss", "json")
	return cmd
}

// notifyReview tells a herdr user, who may be in another workspace while the
// agent explores, that there will be something to review. It is sent when the
// agent records, since exiting the harness is what the user then has to do. A
// notification that fails is no reason to fail the record.
func notifyReview(c *core.Core, p *core.Package, n int) {
	if w := c.FindInit(p); n == 0 || w == nil || !w.Running() || !herdr.Inside() {
		return
	}
	_ = herdr.Notify(p.Name+": "+countCommands(n)+" to review", "Exit the harness to accept, edit or dismiss what it suggested.")
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
			if res.NewPending > 0 {
				notifyReview(c, p, len(res.Pending))
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, res)
			}
			fprintf(out, "recorded for %s: %s\n", p.Name, strings.Join(res.Set, ", "))
			if len(res.Kept) > 0 {
				fprintf(out, "kept the user's: %s\n", strings.Join(res.Kept, ", "))
			}
			if n := len(res.Pending); n > 0 {
				fprintf(out, "%s for the user to review when they exit\n", countCommands(n))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}
