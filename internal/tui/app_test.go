package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestCommandBarRunsCommands(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")

	d.key(":")
	d.typeText("zzz")
	d.key("enter")
	d.has("unknown command: zzz")
	d.key("j") // any key clears the notice
	d.lacks("unknown command")

	d.key(":")
	d.typeText("conf")
	d.has("config") // ghost text completes the command
	d.key("tab")
	d.key("enter")
	d.has("Source root", "Package root")
	d.key("esc")
	d.has("packages[0]")

	d.key(":")
	d.typeText("q")
	d.key("enter")
	if !d.quit {
		t.Error(":q should quit")
	}
}

func TestCommandBarEscClosesWithoutRunning(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")
	d.key(":")
	d.typeText("new")
	d.key("esc")
	d.has("packages[0]")
	d.lacks("new package")
}

func TestFilterBarFiltersLiveAndEscClears(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	seed(t, env, "auth-rework", env.NewSource(t, "beta"))
	d := newDriver(t, env, "")
	d.has("packages[2]")

	d.key("/")
	d.typeText("pay")
	d.has("packages(/pay)[1]", "payments")
	d.lacks("auth-rework")
	d.key("enter") // keeps the filter, closes the bar
	d.has("packages(/pay)[1]")
	d.key("esc") // clears it
	d.has("packages[2]", "auth-rework")
}

func TestPackagesSortKeys(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "bravo", env.NewSource(t, "a"))
	seed(t, env, "alpha", env.NewSource(t, "b"))
	d := newDriver(t, env, "")
	d.key("N")
	d.has("NAME↑")
	if r, _ := d.app.top().(*packagesModel).tb.selected(); r.id != "alpha" {
		t.Errorf("name sort should put alpha first, got %q", r.id)
	}
	d.key("N")
	d.has("NAME↓")
}

func TestHelpIsAModalOverTheScreen(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("?")
	d.has("help", "GLOBAL", "payments") // the table stays visible underneath
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
		d.key("esc")
		d.key("n")
		check("editor")
		d.key("esc")
		d.key("l")
		check("launch")
		d.key("esc")
		d.key(":")
		check("command bar")
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
