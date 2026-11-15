package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestFrameIsExactlySized(t *testing.T) {
	f := renderFrame("packages[2]", "one\ntwo is a much longer line than the frame can hold at all", 24, 6)
	lines := strings.Split(f, "\n")
	if len(lines) != 6 {
		t.Fatalf("frame has %d lines, want 6", len(lines))
	}
	for i, l := range lines {
		if lipgloss.Width(l) != 24 {
			t.Errorf("line %d is %d wide: %q", i, lipgloss.Width(l), l)
		}
	}
	if !strings.Contains(lines[0], "packages[2]") {
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

func TestHeaderHasPlainTitleContextAndHints(t *testing.T) {
	ctx := []kv{{"Package root", "~/.vmono"}, {"Harness", "claude"}}
	hints := []hint{{"o", "Open"}, {"l", "Launch"}, {"d", "Describe"}, {"e", "Edit"}, {"s", "Sync"}}
	for _, w := range []int{120, 60} {
		h := renderHeader(w, ctx, hints)
		lines := strings.Split(h, "\n")
		if len(lines) != headerHeight {
			t.Fatalf("w=%d: header has %d lines, want %d", w, len(lines), headerHeight)
		}
		if !strings.Contains(lines[0], "vmono") || !strings.Contains(h, "Package root:") {
			t.Errorf("w=%d: title and context missing:\n%s", w, h)
		}
		if strings.ContainsAny(h, "█▀▄") {
			t.Errorf("w=%d: no block-art wordmark expected:\n%s", w, h)
		}
		for i, l := range lines {
			if lipgloss.Width(l) > w {
				t.Errorf("w=%d: line %d too wide", w, i)
			}
		}
	}
	if !strings.Contains(renderHeader(120, ctx, hints), "<o>") {
		t.Error("wide header should list the key hints")
	}
}

func TestStatusShowsCrumbsAndFlash(t *testing.T) {
	s := renderStatus(60, []string{"packages", "members"}, "fetched 2", false)
	if lipgloss.Width(s) != 60 || !strings.Contains(s, "<packages>") || !strings.Contains(s, "fetched 2") {
		t.Errorf("status = %q", s)
	}
}
