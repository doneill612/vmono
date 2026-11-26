package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newSyncCmd builds `vmono sync`. Given a session (pkg/session, or from
// inside one) it syncs that session; given a package, or run from a package
// directory, it syncs every session of it.
func newSyncCmd() *cobra.Command {
	var refs, asJSON bool
	cmd := &cobra.Command{
		Use:   "sync [pkg[/session]] [--refs] [--json]",
		Short: "Make session directories match the manifest and regenerate files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadCore()
			if err != nil {
				return err
			}
			p, s, err := c.ResolveContext(packageArg(args))
			if err != nil {
				return err
			}
			var results []core.SessionSync
			events := cmd.ErrOrStderr()
			if asJSON {
				events = discardWriter{}
			}
			opts := core.SyncOptions{Refs: refs}
			serr := withEvents(events, c, func() error {
				if s != nil {
					res, e := c.Sync(s, opts)
					results = []core.SessionSync{{Session: s.Name, SyncResult: res}}
					return e
				}
				var e error
				results, e = c.SyncPackage(p, opts)
				return e
			})
			out := cmd.OutOrStdout()
			if asJSON {
				if results == nil {
					results = []core.SessionSync{}
				}
				if err := printJSON(out, results); err != nil {
					return err
				}
				return serr
			}
			if len(results) == 0 {
				fprintf(out, "no sessions to sync\n")
			}
			for _, r := range results {
				if len(results) > 1 {
					fprintf(out, "%s:\n", r.Session)
				}
				summarize(out, "recreated", r.Recreated)
				summarize(out, "moved to latest ref", r.RefsMoved)
				summarize(out, "not in manifest", r.Unlisted)
				summarize(out, "regenerated", r.Files)
				if len(r.Recreated)+len(r.RefsMoved)+len(r.Unlisted)+len(r.Files) == 0 {
					fprintf(out, "already in sync\n")
				}
			}
			return serr
		},
	}
	cmd.Flags().BoolVar(&refs, "refs", false, "also move clean read-only members to the latest fetched commit of their ref")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON: one entry per session synced")
	return cmd
}

// summarize prints "label: a, b" for a non-empty list.
func summarize(w io.Writer, label string, items []string) {
	if len(items) > 0 {
		fprintf(w, "%s: %s\n", label, strings.Join(items, ", "))
	}
}

// discardWriter drops everything written to it.
type discardWriter struct{}

// Write implements io.Writer.
func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
