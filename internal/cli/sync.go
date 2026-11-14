package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/doneill612/vmono/internal/core"
)

// newSyncCmd builds `vmono sync`.
func newSyncCmd() *cobra.Command {
	var refs, asJSON bool
	cmd := &cobra.Command{
		Use:   "sync [pkg] [--refs] [--json]",
		Short: "Make the directory match the manifest and regenerate files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, p, err := resolve(packageArg(args))
			if err != nil {
				return err
			}
			var res core.SyncResult
			events := cmd.ErrOrStderr()
			if asJSON {
				events = discardWriter{}
			}
			serr := withEvents(events, c, func() error {
				var e error
				res, e = c.Sync(p, core.SyncOptions{Refs: refs})
				return e
			})
			out := cmd.OutOrStdout()
			if asJSON {
				if err := printJSON(out, res); err != nil {
					return err
				}
				return serr
			}
			summarize(out, "recreated", res.Recreated)
			summarize(out, "moved to latest ref", res.RefsMoved)
			summarize(out, "not in manifest", res.Unlisted)
			summarize(out, "regenerated", res.Files)
			if len(res.Recreated)+len(res.RefsMoved)+len(res.Unlisted)+len(res.Files) == 0 {
				fprintf(out, "already in sync\n")
			}
			return serr
		},
	}
	cmd.Flags().BoolVar(&refs, "refs", false, "also move clean reference members to their latest fetched ref")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
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
