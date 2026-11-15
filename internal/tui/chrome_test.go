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

func TestHeaderDropsLogoWhenNarrow(t *testing.T) {
	ctx := []kv{{"Package root", "~/.vmono"}, {"Harness", "claude"}}
	hints := []hint{{"o", "Open"}, {"l", "Launch"}, {"d", "Describe"}, {"e", "Edit"}, {"s", "Sync"}}
	wide := renderHeader(120, ctx, hints)
	if !strings.Contains(wide, "worktree packages") || !strings.Contains(wide, "<o>") {
		t.Errorf("wide header should have logo and hints:\n%s", wide)
	}
	narrow := renderHeader(60, ctx, hints)
	if strings.Contains(narrow, "worktree packages") {
		t.Errorf("narrow header should drop the logo:\n%s", narrow)
	}
	for _, h := range []string{wide, narrow} {
		for i, l := range strings.Split(h, "\n") {
			if lipgloss.Width(l) > 120 {
				t.Errorf("line %d too wide", i)
			}
		}
	}
}

func TestStatusShowsCrumbsAndFlash(t *testing.T) {
	s := renderStatus(60, []string{"packages", "members"}, "fetched 2", false)
	if lipgloss.Width(s) != 60 || !strings.Contains(s, "<packages>") || !strings.Contains(s, "fetched 2") {
		t.Errorf("status = %q", s)
	}
}
