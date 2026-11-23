package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestFrameIsExactlySized(t *testing.T) {
	f := renderFrame("Packages (2)", "one\ntwo is a much longer line than the frame can hold at all", 24, 6)
	lines := strings.Split(f, "\n")
	if len(lines) != 6 {
		t.Fatalf("frame has %d lines, want 6", len(lines))
	}
	for i, l := range lines {
		if lipgloss.Width(l) != 24 {
			t.Errorf("line %d is %d wide: %q", i, lipgloss.Width(l), l)
		}
	}
	if !strings.Contains(lines[0], "Packages (2)") {
		t.Errorf("title should sit in the top border: %q", lines[0])
	}
}

func TestOverlayCompositesOverBackground(t *testing.T) {
	bg := strings.Join([]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"}, "\n")
	got := overlay(bg, "XX\nYY", 3, 1)
	want := strings.Join([]string{"aaaaaaaaaa", "bbbXXbbbbb", "cccYYccccc"}, "\n")
	if got != want {
		t.Errorf("overlay:\n%s\nwant:\n%s", got, want)
	}
	// Styled foreground over a styled background keeps both widths right.
	styledBG := styleDim.Render("0123456789")
	out := overlay(styledBG, "##", 4, 0)
	if ansi.Strip(out) != "0123##6789" {
		t.Errorf("styled overlay = %q", ansi.Strip(out))
	}
}

func TestTopBarShowsCrumbsAndSummary(t *testing.T) {
	for _, w := range []int{120, 40} {
		bar := renderTopBar(w, []string{"packages", "payments"}, "2 packages · claude")
		if lipgloss.Width(bar) != w {
			t.Errorf("w=%d: top bar is %d wide: %q", w, lipgloss.Width(bar), bar)
		}
		plain := ansi.Strip(bar)
		if !strings.Contains(plain, "vmono") || !strings.Contains(plain, "packages › payments") {
			t.Errorf("w=%d: badge and crumbs missing: %q", w, plain)
		}
	}
	if wide := ansi.Strip(renderTopBar(120, []string{"packages"}, "2 packages · claude")); !strings.HasSuffix(strings.TrimRight(wide, " "), "claude") {
		t.Errorf("the summary belongs at the right edge: %q", wide)
	}
}

func TestFooterListsKeysAndDropsThemWhenNarrow(t *testing.T) {
	hints := []hint{{"enter", "actions"}, {"n", "new"}, {"/", "filter"}, {"?", "help"}}
	wide := ansi.Strip(renderFooter(80, hints, "", false))
	for _, h := range hints {
		if !strings.Contains(wide, h.key+" "+h.label) {
			t.Errorf("footer missing %q: %q", h.key, wide)
		}
	}
	narrow := renderFooter(24, hints, "", false)
	if lipgloss.Width(narrow) != 24 {
		t.Fatalf("narrow footer is %d wide", lipgloss.Width(narrow))
	}
	if p := ansi.Strip(narrow); !strings.Contains(p, "enter actions") || strings.Contains(p, "help") {
		t.Errorf("keys should drop from the end: %q", p)
	}
}

func TestFooterNoticeTakesRoomFromTheKeys(t *testing.T) {
	hints := []hint{{"enter", "actions"}, {"n", "new"}, {"/", "filter"}}
	s := ansi.Strip(renderFooter(60, hints, "fetched 2 member(s)", false))
	if lipgloss.Width(s) != 60 || !strings.Contains(s, "fetched 2 member(s)") || !strings.Contains(s, "enter actions") {
		t.Errorf("footer = %q", s)
	}
}
