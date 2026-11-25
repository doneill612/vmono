package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette uses adaptive colors so the TUI reads on light and dark terminals.
// The chrome is deliberately quiet: dim gray for borders, column heads and
// secondary text, one accent for the badge, the selection and dialog titles,
// and the green, amber and red trio reserved for state.
var (
	colAccent = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#5FD7FF"}
	colFrame  = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#4B5563"}
	colSelBg  = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#262B33"}
	colSelFg  = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#000000"}
	colDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	colOK     = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	colErr    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
)

var (
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleDim      = lipgloss.NewStyle().Foreground(colDim)
	styleOK       = lipgloss.NewStyle().Foreground(colOK)
	styleWarn     = lipgloss.NewStyle().Foreground(colWarn)
	styleErr      = lipgloss.NewStyle().Foreground(colErr)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleLabel    = lipgloss.NewStyle().Bold(true)

	styleAmber      = lipgloss.NewStyle().Foreground(colDim)
	styleCyan       = lipgloss.NewStyle().Foreground(colAccent)
	styleFrame      = lipgloss.NewStyle().Foreground(colFrame)
	styleFrameTitle = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleHeaderCol  = lipgloss.NewStyle().Bold(true).Foreground(colDim)
	styleColHead    = lipgloss.NewStyle().Foreground(colDim)
	styleKey        = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleBadge      = lipgloss.NewStyle().Bold(true).Background(colAccent).Foreground(colSelFg)
	styleSelBg      = lipgloss.NewStyle().Bold(true).Background(colSelBg)
	styleSelGutter  = lipgloss.NewStyle().Foreground(colAccent).Background(colSelBg)
	styleSelRow     = lipgloss.NewStyle().Bold(true).Background(colSelBg)
)

// keyHelp renders a footer line of "key action" hints.
func keyHelp(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, styleLabel.Render(pairs[i])+" "+styleDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, styleDim.Render("  ·  "))
}

// ago formats a past time as a short relative string.
func ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// truncate shortens s to n display cells with an ellipsis.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= n {
		return s
	}
	r := []rune(s)
	if n == 1 {
		return string(r[:1])
	}
	for len(r) > 0 && lipgloss.Width(string(r))+1 > n {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// hardWrap breaks s into lines of at most width cells, inside words too, so
// that all of it shows.
func hardWrap(s string, width int) string { return ansi.Hardwrap(s, width, true) }

// wrap hard-wraps text to width, preserving existing line breaks.
func wrap(s string, width int) string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		cur := words[0]
		for _, w := range words[1:] {
			if lipgloss.Width(cur)+1+lipgloss.Width(w) > width {
				out = append(out, cur)
				cur = w
			} else {
				cur += " " + w
			}
		}
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}
