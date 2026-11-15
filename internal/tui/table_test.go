package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// testRows builds n rows named r0..r(n-1) with a numeric second column.
func testRows(names ...string) []row {
	var rows []row
	for i, n := range names {
		rows = append(rows, row{id: n, search: n, cells: []cell{plain(n), keyed(strings.Repeat("x", i+1), numKey(i), lipgloss.NewStyle())}})
	}
	return rows
}

func TestTableSortTogglesAndKeepsSelection(t *testing.T) {
	tb := newTable([]column{{title: "name", flex: true}, {title: "size"}})
	tb.setRows(testRows("bravo", "alpha", "charlie"))
	tb.move(1) // alpha is second in input order, so cursor is on it
	if id := tb.selectedID(); id != "alpha" {
		t.Fatalf("selected = %q", id)
	}
	tb.sortBy(0)
	if tb.rows[0].id != "alpha" || tb.rows[2].id != "charlie" {
		t.Fatalf("ascending order wrong: %v", ids(tb.rows))
	}
	if tb.selectedID() != "alpha" {
		t.Errorf("sort should keep the cursor on alpha, got %q", tb.selectedID())
	}
	tb.sortBy(0)
	if tb.rows[0].id != "charlie" {
		t.Fatalf("second press should reverse: %v", ids(tb.rows))
	}
	if v := tb.view(40, 6); !strings.Contains(v, "NAME↓") {
		t.Errorf("header should show the sort arrow:\n%s", v)
	}
	tb.sortBy(1) // numeric key: input order
	if tb.rows[0].id != "bravo" {
		t.Fatalf("numeric key order wrong: %v", ids(tb.rows))
	}
}

func TestTableFilterAndScroll(t *testing.T) {
	var names []string
	for _, n := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		names = append(names, n)
	}
	tb := newTable([]column{{title: "name", flex: true}})
	tb.setRows(testRows(names...))
	tb.bottom()
	v := tb.view(30, 4) // header + 3 rows
	if !strings.Contains(v, "eight") || strings.Contains(v, "one") {
		t.Errorf("cursor at the bottom should scroll it into view:\n%s", v)
	}
	tb.setFilter("sev")
	if len(tb.rows) != 1 || tb.rows[0].id != "seven" {
		t.Fatalf("filter result: %v", ids(tb.rows))
	}
	tb.setFilter("")
	if len(tb.rows) != len(names) {
		t.Errorf("clearing the filter should restore rows")
	}
}

func TestTableViewFitsWidth(t *testing.T) {
	tb := newTable([]column{{title: "name"}, {title: "branch", flex: true}, {title: "n", right: true}})
	tb.setRows([]row{{id: "a", cells: []cell{plain("alpha"), plain(strings.Repeat("b", 80)), plain("3")}}})
	for _, w := range []int{30, 60, 100} {
		for i, line := range strings.Split(tb.view(w, 5), "\n") {
			if lipgloss.Width(line) > w {
				t.Errorf("w=%d line %d is %d wide: %q", w, i, lipgloss.Width(line), line)
			}
		}
	}
}

func TestTableEmptyMessage(t *testing.T) {
	tb := newTable([]column{{title: "name"}})
	tb.empty = "Nothing here."
	if v := tb.view(30, 5); !strings.Contains(v, "Nothing here.") {
		t.Errorf("empty message missing:\n%s", v)
	}
}

// ids lists row ids for failure messages.
func ids(rows []row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.id)
	}
	return out
}
