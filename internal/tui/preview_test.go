package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestPreviewBesideTheListFollowsTheHighlight(t *testing.T) {
	env := testutil.NewEnv(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "payments", a, b)
	seed(t, env, "auth", env.NewSource(t, "gamma"))
	d := newDriver(t, env, "")

	// auth sorts first and is highlighted: its members, not payments'.
	d.has("Purpose of auth", "gamma")
	d.lacks("alpha", "beta")
	d.key("j")
	d.has("Purpose of payments", "alpha", "beta")
	d.lacks("gamma")
	d.key("k")
	d.has("gamma") // back on a row already loaded

	d.key("right")
	d.has("branch", "vmono/auth/work", "MEMBERS", "gamma")
	d.key("m")
	d.has("built on", "origin/HEAD", "no commits yet")
}

func TestNarrowTerminalsGetTheListAlone(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.send(tea.WindowSizeMsg{Width: previewMinW - 1, Height: 30})
	d.has("payments")
	d.lacks("Purpose of payments", "MEMBERS")
	d.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	d.key("j", "k") // any message lets the pane catch up
	d.has("Purpose of payments", "MEMBERS", "alpha")
}

func TestPreviewLoadsAgainWhenTheViewReloads(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.has("clean")
	pk := d.app.top().(*packagesModel)
	if len(pk.pv.built) == 0 {
		t.Fatal("the highlighted package's preview should be loaded")
	}
	d.key("ctrl+r")
	if len(pk.pv.built) == 0 {
		t.Error("a reload should load the preview again")
	}
}
