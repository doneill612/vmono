package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// hint is one key and what it does, shown in the footer and the help dialog.
type hint struct{ key, label string }

// clip truncates s to w cells and pads it to exactly w, keeping ANSI
// styling intact.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "")
	if pad := w - lipgloss.Width(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// renderFrame draws body inside a rounded box with the title set into the top
// border at the left. The box is exactly w by h cells; body is cut or padded
// to fit. Only dialogs are framed; the screens themselves are not.
func renderFrame(title, body string, w, h int) string {
	if w < 4 || h < 2 {
		return ""
	}
	inner := w - 2
	t := ""
	if title != "" {
		t = " " + truncate(title, inner-4) + " "
	}
	var b strings.Builder
	b.WriteString(styleFrame.Render("╭─") + styleFrameTitle.Render(t) +
		styleFrame.Render(strings.Repeat("─", max(inner-1-lipgloss.Width(t), 0))+"╮"))
	lines := strings.Split(body, "\n")
	edge := styleFrame.Render("│")
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString("\n" + edge + clip(line, inner) + edge)
	}
	b.WriteString("\n" + styleFrame.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return b.String()
}

// dialog draws a framed box sized to its content, for modals. It is as wide
// as the widest line plus a margin, never wider than maxW and never
// narrower than the title needs.
func dialog(title, body string, maxW int) string {
	lines := strings.Split(body, "\n")
	w := lipgloss.Width(title) + 6
	for _, l := range lines {
		if n := lipgloss.Width(l) + 3; n > w {
			w = n
		}
	}
	if w > maxW {
		w = maxW
	}
	return renderFrame(title, body, w, len(lines)+2)
}

// overlay draws fg over bg with fg's top-left corner at column x, row y.
// Both are multi-line strings that may carry ANSI styling.
func overlay(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	for i, fl := range strings.Split(fg, "\n") {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		fw := lipgloss.Width(fl)
		base := bgLines[row]
		if bw := lipgloss.Width(base); bw < x+fw {
			base += strings.Repeat(" ", x+fw-bw)
		}
		bgLines[row] = ansi.Truncate(base, x, "") + fl + ansi.TruncateLeft(base, x+fw, "")
	}
	return strings.Join(bgLines, "\n")
}

// renderTopBar draws the single line at the top: the app badge, the
// breadcrumb trail leading to the current view with the last one emphasized,
// and a short summary at the right edge.
func renderTopBar(w int, crumbs []string, summary string) string {
	left := " " + styleBadge.Render(" vmono ")
	for i, c := range crumbs {
		sep := styleDim.Render(" › ")
		if i == 0 {
			sep = "  "
		}
		st := styleDim
		if i == len(crumbs)-1 {
			st = styleLabel
		}
		left += sep + st.Render(c)
	}
	right := styleDim.Render(summary) + " "
	space := w - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		return clip(left, w)
	}
	return clip(left+strings.Repeat(" ", space)+right, w)
}

// renderRule draws a full-width dim rule.
func renderRule(w int) string { return styleDim.Render(strings.Repeat("─", w)) }

// renderFooter draws the bottom line: the keys worth knowing here at the
// left, as many as fit, and the notice at the right. A notice takes room from
// the keys rather than the other way round, since it is what just happened.
func renderFooter(w int, hints []hint, notice string, isErr bool) string {
	right := ""
	if notice != "" {
		st := styleOK
		if isErr {
			st = styleErr
		}
		right = st.Render(truncate(notice, max(w-6, 1))) + " "
	}
	left := " "
	avail := w - lipgloss.Width(right) - 2
	for i, h := range hints {
		item := styleLabel.Render(h.key) + " " + styleDim.Render(h.label)
		if i > 0 {
			item = "  " + item
		}
		if lipgloss.Width(left)+lipgloss.Width(item) > avail {
			break
		}
		left += item
	}
	space := w - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		return clip(left, w)
	}
	return clip(left+strings.Repeat(" ", space)+right, w)
}
