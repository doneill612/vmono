package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// hint is one key and what it does, shown in the header and the help dialog.
type hint struct{ key, label string }

// kv is one labelled line of header context.
type kv struct{ k, v string }

// headerHeight is the number of lines the full header takes: the title,
// then one line per context value.
const headerHeight = 5

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

// renderFrame draws body inside a box with the title set into the top
// border. The box is exactly w by h cells; body is cut or padded to fit.
func renderFrame(title, body string, w, h int) string {
	if w < 4 || h < 2 {
		return ""
	}
	inner := w - 2
	t := ""
	if title != "" {
		t = " " + truncate(title, inner-2) + " "
	}
	tw := lipgloss.Width(t)
	left := (inner - tw) / 2
	right := inner - tw - left
	var b strings.Builder
	b.WriteString(styleFrame.Render("┌"+strings.Repeat("─", left)) +
		styleFrameTitle.Render(t) +
		styleFrame.Render(strings.Repeat("─", right)+"┐"))
	lines := strings.Split(body, "\n")
	edge := styleFrame.Render("│")
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString("\n" + edge + clip(line, inner) + edge)
	}
	b.WriteString("\n" + styleFrame.Render("└"+strings.Repeat("─", inner)+"┘"))
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

// renderHeader draws the five-line header: the title and the context
// values at the left, a vertical rule, then the current view's key hints in
// columns. Hint columns that do not fit are dropped.
func renderHeader(w int, ctx []kv, ctxHints, hints []hint) string {
	// ctxHints act on the context values, so they sit on the title line
	// above them, at the right edge of that block, rather than among the
	// hints.
	var ctxHint string
	for i, h := range ctxHints {
		if i > 0 {
			ctxHint += "  "
		}
		ctxHint += styleKey.Render("<"+h.key+">") + " " + styleDim.Render(h.label)
	}
	leftW := 0
	for _, c := range ctx {
		if n := 1 + 14 + lipgloss.Width(c.v); n > leftW {
			leftW = n
		}
	}
	if n := 1 + lipgloss.Width("vmono") + 3 + lipgloss.Width(ctxHint); ctxHint != "" && n > leftW {
		leftW = n
	}
	leftW += 2
	if leftW > w/2 {
		leftW = w / 2
	}
	room := w - leftW - 2

	// Hint columns, one header's height to a column.
	var cols []string
	if room > 0 {
		for start := 0; start < len(hints); start += headerHeight {
			end := start + headerHeight
			if end > len(hints) {
				end = len(hints)
			}
			chunk := hints[start:end]
			keyW, labW := 0, 0
			for _, h := range chunk {
				if n := lipgloss.Width(h.key) + 2; n > keyW {
					keyW = n
				}
				if n := lipgloss.Width(h.label); n > labW {
					labW = n
				}
			}
			colW := keyW + 1 + labW + 3
			used := 0
			for _, c := range cols {
				used += lipgloss.Width(strings.SplitN(c, "\n", 2)[0])
			}
			if used+colW > room {
				break
			}
			var lines []string
			for i := 0; i < headerHeight; i++ {
				l := ""
				if i < len(chunk) {
					k := "<" + chunk[i].key + ">"
					l = styleKey.Render(fit(k, keyW, false)) + " " + styleDim.Render(chunk[i].label)
				}
				lines = append(lines, clip(l, colW))
			}
			cols = append(cols, strings.Join(lines, "\n"))
		}
	}
	colLines := make([][]string, len(cols))
	for i, c := range cols {
		colLines[i] = strings.Split(c, "\n")
	}

	var b strings.Builder
	for i := 0; i < headerHeight; i++ {
		left := ""
		textW := leftW - 2
		if len(colLines) > 0 {
			textW = leftW - 3
		}
		switch {
		case i == 0:
			left = " " + styleTitle.Render("vmono")
			if gap := textW - lipgloss.Width(left) - lipgloss.Width(ctxHint); ctxHint != "" && gap >= 2 {
				left += strings.Repeat(" ", gap) + ctxHint
			}
		case i-1 < len(ctx):
			left = " " + styleAmber.Render(fit(ctx[i-1].k+":", 14, false)) + ctx[i-1].v
		}
		line := clip(left, textW) + "  "
		if len(colLines) > 0 {
			// A rule keeps the context values from reading as the labels
			// of the hints beside them.
			line = clip(left, textW) + " " + styleFrame.Render("│") + " "
		}
		for _, c := range colLines {
			line += c[i]
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(clip(line, w))
	}
	return b.String()
}

// renderStatus draws the bottom line: breadcrumb chips at the left and the
// current flash message at the right.
func renderStatus(w int, crumbs []string, flash string, isErr bool) string {
	var chips []string
	for i, c := range crumbs {
		st := styleCrumb
		if i == len(crumbs)-1 {
			st = styleCrumbLast
		}
		chips = append(chips, st.Render("<"+c+">"))
	}
	left := " " + strings.Join(chips, " ")
	right := ""
	if flash != "" {
		st := styleOK
		if isErr {
			st = styleErr
		}
		right = st.Render(truncate(flash, w-lipgloss.Width(left)-3)) + " "
	} else {
		right = styleKey.Render("<:>") + styleDim.Render(" command  ") + styleKey.Render("<?>") + styleDim.Render(" help ")
	}
	space := w - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		return clip(left, w)
	}
	return clip(left+strings.Repeat(" ", space)+right, w)
}
