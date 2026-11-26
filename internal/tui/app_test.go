package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestPaletteRunsEntries(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")

	d.key(":")
	d.has("go to…", "Packages", "Repos", "Harnesses", "Settings")
	d.typeText("zzz")
	d.has("no match")
	d.key("enter") // nothing to run
	d.has("no match")
	d.key("esc")
	d.lacks("go to…")

	d.key("ctrl+p") // the second way in
	d.typeText("sett")
	d.key("enter")
	d.has("Source root", "Package root")
	d.key("esc")
	d.has("Packages (0)")

	d.key(":")
	d.typeText("quit")
	d.has("Quit")
	d.key("enter")
	if !d.quit {
		t.Error("the Quit entry should quit")
	}
}

func TestPaletteEscClosesWithoutRunning(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")
	d.key(":")
	d.typeText("new")
	d.key("esc")
	d.has("Packages (0)")
	d.lacks("New package")
}

func TestPaletteOffersTheSelectedRowsActions(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key(":")
	d.has("Launch latest", "Sync all")
	d.typeText("sessions of")
	d.has("Sessions of payments")
	d.key("esc", ":")
	d.typeText("descr")
	d.key("enter")
	d.has("Describe · payments")
}

func TestFilterBarFiltersLiveAndEscClears(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	seed(t, env, "auth-rework", env.NewSource(t, "beta"))
	d := newDriver(t, env, "")
	d.has("Packages (2)")

	d.key("/")
	d.typeText("pay")
	d.has("Packages /pay (1)", "payments")
	d.lacks("auth-rework")
	d.key("enter") // keeps the filter, closes the bar
	d.has("Packages /pay (1)")
	d.key("esc") // clears it
	d.has("Packages (2)", "auth-rework")
}

func TestPaletteSortsTheTable(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "bravo", env.NewSource(t, "a"))
	seed(t, env, "alpha", env.NewSource(t, "b"))
	d := newDriver(t, env, "")
	sortByName := func() { d.key(":"); d.typeText("sort by name"); d.key("enter") }
	sortByName()
	d.has("name↑")
	if r, _ := d.app.top().(*packagesModel).tb.selected(); r.id != "alpha" {
		t.Errorf("name sort should put alpha first, got %q", r.id)
	}
	sortByName()
	d.has("name↓")
}

func TestHelpIsAModalOverTheScreen(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("?")
	d.has("help", "GLOBAL", "payments") // the table stays visible underneath
	d.has("what vmono's terms mean")
	d.key("t")
	d.has("TERMS", "read-only", "build on", "work on")
	d.lacks("GLOBAL")
	d.key("t")
	d.has("GLOBAL")
	d.key("esc")
	d.lacks("GLOBAL")
}

// TestEveryScreenFitsTheTerminal renders the main screens at several sizes
// and checks no line is wider, and the output no taller, than the terminal.
func TestEveryScreenFitsTheTerminal(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	for _, sz := range [][2]int{{120, 40}, {80, 24}, {60, 16}} {
		d := newDriver(t, env, "")
		d.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		check := func(what string) {
			t.Helper()
			lines := strings.Split(d.view(), "\n")
			if len(lines) > sz[1] {
				t.Errorf("%s at %dx%d: %d lines", what, sz[0], sz[1], len(lines))
			}
			for i, l := range lines {
				if lipgloss.Width(l) > sz[0] {
					t.Errorf("%s at %dx%d: line %d is %d wide: %q", what, sz[0], sz[1], i, lipgloss.Width(l), l)
				}
			}
		}
		check("packages")
		d.key("?")
		check("help")
		d.key("t")
		check("terms")
		d.key("esc")
		d.key("n")
		check("editor")
		d.key("esc")
		d.key("l")
		check("launch")
		d.key("esc")
		d.key(":")
		check("palette")
		d.key("esc")
		d.key("ctrl+d")
		check("delete")
		d.key("esc")
		d.key("enter")
		check("members")
		d.key("d")
		check("member describe")
		d.key("esc", "esc")
		d.key("d")
		check("package describe")
		d.key("esc")
		for _, v := range []string{"repos", "harnesses"} {
			goToView(d, v)
			check(v)
		}
		d.key("a")
		check("finder")
		d.key("esc", "esc")
	}
}
