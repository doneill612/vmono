package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestAccentPlainWhenNotATerminal checks that --version output written to a
// non-terminal carries no escape codes, which scripts and the release smoke
// test rely on.
func TestAccentPlainWhenNotATerminal(t *testing.T) {
	out, _, err := run(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\x1b") {
		t.Fatalf("--version to a non-terminal contains escape codes: %q", out)
	}
}

// TestAccentColorsOnATerminal checks that the accent style does emit color
// when the renderer has a color profile, so the plain-output test above is
// not passing merely because styling is broken.
func TestAccentColorsOnATerminal(t *testing.T) {
	r := lipgloss.NewRenderer(&strings.Builder{})
	r.SetColorProfile(termenv.TrueColor)
	got := r.NewStyle().Bold(true).Foreground(colAccent).Render("0.1.0")
	if !strings.Contains(got, "\x1b[") || !strings.Contains(got, "0.1.0") {
		t.Fatalf("accent style rendered %q, want colored 0.1.0", got)
	}
}
