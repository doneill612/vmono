package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

// column describes one table column. Widths are derived from the content:
// the widest cell (or the title) wins, capped at max when set. A flex column
// is the one that gives up width when the table is too narrow and that
// absorbs spare width when it is wide.
type column struct {
	title string
	max   int
	flex  bool
	right bool
}

// cell is one table cell: plain text, the style it is drawn with when its
// row is not selected, and an optional sort key for text that does not sort
// well as written (numbers, times).
type cell struct {
	text  string
	style lipgloss.Style
	key   string
}

// plain builds an unstyled cell.
func plain(s string) cell { return cell{text: s} }

// styled builds a cell with a style.
func styled(s string, st lipgloss.Style) cell { return cell{text: s, style: st} }

// keyed builds a cell whose sort key differs from its text.
func keyed(s, key string, st lipgloss.Style) cell { return cell{text: s, style: st, key: key} }

// numKey returns a sort key that orders numbers correctly as strings.
func numKey(n int) string { return fmt.Sprintf("%012d", n) }

// row is one table row. id identifies it across reloads so the cursor stays
// on the same item; search is the text the filter matches against.
type row struct {
	id     string
	cells  []cell
	search string
}

// gap is the space between columns.
const gap = "  "

// table is the list widget behind every view: a header with sort markers,
// rows with a full-width selection bar, a cursor with scrolling, and a
// fuzzy filter. The selected row is drawn from plain text so the bar's
// colors are never broken up by per-cell styles.
type table struct {
	cols     []column
	all      []row
	rows     []row
	cursor   int
	offset   int
	page     int
	sortCol  int
	sortDesc bool
	filter   string
	// empty is shown in place of rows when there are none.
	empty string
}

// newTable builds an empty table with no sort applied.
func newTable(cols []column) *table {
	return &table{cols: cols, sortCol: -1, page: 10}
}

// setRows replaces the rows, keeping the cursor on the same row id when it
// still exists.
func (t *table) setRows(rows []row) {
	id := t.selectedID()
	t.all = rows
	t.rebuild()
	t.restore(id)
}

// restore moves the cursor to the row with the given id, else clamps it.
func (t *table) restore(id string) {
	if id != "" {
		for i, r := range t.rows {
			if r.id == id {
				t.cursor = i
				return
			}
		}
	}
	t.clamp()
}

// rebuild applies the filter and then the sort to produce the visible rows.
func (t *table) rebuild() {
	rows := t.all
	if q := strings.TrimSpace(t.filter); q != "" {
		haystack := make([]string, len(t.all))
		for i, r := range t.all {
			haystack[i] = r.search
		}
		hit := map[int]bool{}
		for _, m := range fuzzy.Find(q, haystack) {
			hit[m.Index] = true
		}
		rows = nil
		for i, r := range t.all {
			if hit[i] {
				rows = append(rows, r)
			}
		}
	}
	rows = append([]row(nil), rows...)
	if t.sortCol >= 0 && t.sortCol < len(t.cols) {
		c := t.sortCol
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := sortKey(rows[i], c), sortKey(rows[j], c)
			if t.sortDesc {
				return a > b
			}
			return a < b
		})
	}
	t.rows = rows
}

// sortKey returns the key a row sorts by in a column.
func sortKey(r row, col int) string {
	if col >= len(r.cells) {
		return ""
	}
	c := r.cells[col]
	if c.key != "" {
		return c.key
	}
	return strings.ToLower(c.text)
}

// setFilter applies a fuzzy filter and puts the cursor back on top.
func (t *table) setFilter(q string) {
	t.filter = q
	t.rebuild()
	t.cursor, t.offset = 0, 0
}

// sortBy sorts by a column, flipping direction when it is already the sort
// column.
func (t *table) sortBy(col int) {
	if col < 0 || col >= len(t.cols) {
		return
	}
	id := t.selectedID()
	if t.sortCol == col {
		t.sortDesc = !t.sortDesc
	} else {
		t.sortCol, t.sortDesc = col, false
	}
	t.rebuild()
	t.restore(id)
}

// selected returns the row under the cursor.
func (t *table) selected() (row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return row{}, false
	}
	return t.rows[t.cursor], true
}

// selectedID returns the id of the row under the cursor, or "".
func (t *table) selectedID() string {
	r, ok := t.selected()
	if !ok {
		return ""
	}
	return r.id
}

// clamp keeps the cursor inside the rows.
func (t *table) clamp() {
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// move shifts the cursor by d rows.
func (t *table) move(d int) {
	t.cursor += d
	t.clamp()
}

// top and bottom jump to either end.
func (t *table) top()    { t.cursor = 0 }
func (t *table) bottom() { t.cursor = len(t.rows) - 1; t.clamp() }

// handleKey applies the navigation keys every list view shares and reports
// whether the key was one of them.
func (t *table) handleKey(k string) bool {
	switch k {
	case "up", "k":
		t.move(-1)
	case "down", "j":
		t.move(1)
	case "pgup", "ctrl+u":
		t.move(-t.page)
	case "pgdown", "ctrl+f":
		t.move(t.page)
	case "home", "g":
		t.top()
	case "end", "G":
		t.bottom()
	default:
		return false
	}
	return true
}

// widths computes column widths for a total width. Columns start at their
// natural width; when that is too wide the flex columns shrink, and when
// there is room to spare the first flex column takes it.
func (t *table) widths(w int) []int {
	ws := make([]int, len(t.cols))
	for i, c := range t.cols {
		ws[i] = lipgloss.Width(c.title) + 2
		for _, r := range t.rows {
			if i < len(r.cells) {
				if cw := lipgloss.Width(r.cells[i].text); cw > ws[i] {
					ws[i] = cw
				}
			}
		}
		if c.max > 0 && ws[i] > c.max {
			ws[i] = c.max
		}
	}
	total := len(gap) * (len(t.cols) - 1)
	for _, x := range ws {
		total += x
	}
	avail := w - 1
	if over := total - avail; over > 0 {
		for i := len(t.cols) - 1; i >= 0 && over > 0; i-- {
			if !t.cols[i].flex {
				continue
			}
			floor := lipgloss.Width(t.cols[i].title) + 2
			if floor < 8 {
				floor = 8
			}
			cut := ws[i] - floor
			if cut > over {
				cut = over
			}
			if cut > 0 {
				ws[i] -= cut
				over -= cut
			}
		}
	} else if spare := -over; spare > 0 {
		for i, c := range t.cols {
			if c.flex {
				ws[i] += spare
				break
			}
		}
	}
	return ws
}

// fit truncates s to w cells and pads it out to exactly w.
func fit(s string, w int, right bool) string {
	s = truncate(s, w)
	pad := w - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}

// view renders the header and as many rows as fit in h lines, scrolled so
// the cursor is visible.
func (t *table) view(w, h int) string {
	if h < 1 || w < 4 {
		return ""
	}
	ws := t.widths(w)
	t.page = h - 2
	if t.page < 1 {
		t.page = 1
	}

	heads := make([]string, len(t.cols))
	for i, c := range t.cols {
		title := strings.ToUpper(c.title)
		if i == t.sortCol {
			if t.sortDesc {
				title += "↓"
			} else {
				title += "↑"
			}
		}
		heads[i] = fit(title, ws[i], c.right)
	}
	var b strings.Builder
	b.WriteString(" " + styleHeaderCol.Render(strings.Join(heads, gap)))

	if len(t.rows) == 0 {
		if t.empty != "" {
			b.WriteString("\n\n " + styleDim.Render(wrap(t.empty, w-2)))
		}
		return b.String()
	}

	visible := h - 1
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+visible {
		t.offset = t.cursor - visible + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
	for i := t.offset; i < len(t.rows) && i-t.offset < visible; i++ {
		r := t.rows[i]
		b.WriteString("\n")
		if i == t.cursor {
			parts := make([]string, len(t.cols))
			for c := range t.cols {
				text := ""
				if c < len(r.cells) {
					text = r.cells[c].text
				}
				parts[c] = fit(text, ws[c], t.cols[c].right)
			}
			b.WriteString(styleSelRow.Width(w).Render(" " + strings.Join(parts, gap)))
			continue
		}
		parts := make([]string, len(t.cols))
		for c := range t.cols {
			cl := cell{}
			if c < len(r.cells) {
				cl = r.cells[c]
			}
			parts[c] = cl.style.Render(fit(cl.text, ws[c], t.cols[c].right))
		}
		b.WriteString(" " + strings.Join(parts, gap))
	}
	return b.String()
}
