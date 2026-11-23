package cli

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/doneill612/vmono/internal/buildinfo"
	"github.com/spf13/cobra"
)

// colAccent is the amber the TUI uses for labels and headings on dark
// backgrounds (colAccent in internal/tui/theme.go), so the CLI and TUI share
// one accent color. It is a fixed color rather than an AdaptiveColor on
// purpose: adaptive colors make the terminal answer a background-color query,
// and a one-shot command like --version should never wait on that.
var colAccent = lipgloss.Color("#FFAF00")

// accent renders s in the accent color, bold, for the terminal behind w.
// The renderer inspects w itself: when w is not a terminal, or NO_COLOR is
// set, or TERM is dumb, s comes back unchanged, so piped output never
// contains escape codes.
func accent(w io.Writer, s string) string {
	return lipgloss.NewRenderer(w).NewStyle().Bold(true).Foreground(colAccent).Render(s)
}

// versionLine is the text printed by --version, "vmono (<version>)": the
// program name in the default color and the version in the accent color. Colors apply only when
// the command's stdout is a terminal.
func versionLine(cmd *cobra.Command) string {
	return "vmono (" + accent(cmd.OutOrStdout(), buildinfo.Version()) + ")"
}

func init() {
	cobra.AddTemplateFunc("vmonoVersion", versionLine)
}
