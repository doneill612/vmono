package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestBranchMarkerSaysHowTheLocalBranchDiffers(t *testing.T) {
	behind := divergence{member: "alpha", counterpart: "origin/main", behind: 3}
	ahead := divergence{member: "beta", counterpart: "origin/main", ahead: 2}
	for want, ch := range map[string]branchChoice{
		"": {pickedBranch: pickedBranch{members: []string{"alpha"}}},
		"3 behind origin/main": {
			pickedBranch: pickedBranch{members: []string{"alpha"}},
			apart:        []divergence{behind},
		},
		"alpha: 3 behind origin/main": {
			pickedBranch: pickedBranch{members: []string{"alpha", "beta"}},
			apart:        []divergence{behind},
		},
		"differs from the remote in alpha, beta": {
			pickedBranch: pickedBranch{members: []string{"alpha", "beta"}},
			apart:        []divergence{behind, ahead},
		},
	} {
		if got := ch.marker(); got != want {
			t.Errorf("marker = %q, want %q", got, want)
		}
	}
	if got := ahead.String(); got != "2 ahead of origin/main" {
		t.Errorf("ahead = %q", got)
	}
	long := divergence{counterpart: "origin/feat/a-rather-long-branch-name", behind: 1}
	if got := long.String(); got != "1 behind origin" {
		t.Errorf("long = %q", got)
	}
}

func TestBranchRowGivesUpDetailBeforeTheMarkerAndName(t *testing.T) {
	const label, marker, detail = "feature/a-rather-long-name", "2 ahead, 3 behind origin/main", "alpha billing-service · 2d ago"
	for _, width := range []int{100, 69, 44, 30, 12} {
		l, m, d := fitRow(width, label, marker, detail)
		used := lipgloss.Width(l) + lipgloss.Width(d)
		if m != "" {
			used += 2 + lipgloss.Width(m)
		}
		if d != "" {
			used += 2
		}
		if used > width {
			t.Errorf("at %d: %q %q %q is %d wide", width, l, m, d, used)
		}
		if width >= 69 && (l != label || m != marker) {
			t.Errorf("at %d the name and marker should be whole: %q %q", width, l, m)
		}
		if width >= 30 && l != label {
			t.Errorf("at %d the name should be whole: %q", width, l)
		}
	}
}
