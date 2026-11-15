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

// logoLines is the wordmark drawn at the right of the header.
var logoLines = []string{
	"█ █ █▀▄▀█ █▀█ █▄ █ █▀█",
	"▀▄▀ █ ▀ █ ▀▀▀ █ ▀█ ▀▀▀",
	"  worktree packages   ",
}

// logoWidth is the cell width of the wordmark.
const logoWidth = 22

// headerHeight is the number of lines the full header takes.
const headerHeight = 4

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

// dialog draws a framed box sized to its content, for modals. w is the
// total width including the border.
func dialog(title, body string, w int) string {
	lines := strings.Count(body, "\n") + 1
	return renderFrame(title, body, w, lines+2)
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

// renderHeader draws the four-line header: context at the left, key hints
// in columns in the middle, the wordmark at the right. Hint columns that do
// not fit are dropped, and the wordmark goes first on narrow terminals.
func renderHeader(w int, ctx []kv, hints []hint) string {
	leftW := 0
	for _, c := range ctx {
		if n := len(c.k) + 2 + lipgloss.Width(c.v); n > leftW {
			leftW = n
		}
	}
	leftW += 2
	if leftW > w/3 {
		leftW = w / 3
	}
	showLogo := w >= leftW+logoWidth+30
	room := w - leftW - 2
	if showLogo {
		room -= logoWidth + 1
	}

	// Hint columns, four to a column.
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
		if i < len(ctx) {
			left = " " + styleAmber.Render(fit(ctx[i].k+":", 14, false)) + ctx[i].v
		}
		line := clip(left, leftW)
		for _, c := range colLines {
			line += c[i]
		}
		if showLogo {
			line = clip(line, w-logoWidth-1)
			if i < len(logoLines) {
				style := styleAmber
				if i == 2 {
					style = styleDim
				}
				line += style.Render(logoLines[i])
			}
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
	if flash == "" {
		return clip(left, w)
	}
	st := styleOK
	if isErr {
		st = styleErr
	}
	right := st.Render(truncate(flash, w-lipgloss.Width(left)-3)) + " "
	space := w - lipgloss.Width(left) - lipgloss.Width(right)
	if space < 1 {
		space = 1
	}
	return clip(left+strings.Repeat(" ", space)+right, w)
}
