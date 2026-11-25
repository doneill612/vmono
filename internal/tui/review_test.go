package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// suggest records setup commands for a package's first member.
func suggest(t *testing.T, c *core.Core, pkg string, commands ...string) {
	t.Helper()
	p, err := c.Load(pkg)
	if err != nil {
		t.Fatal(err)
	}
	rec := core.InitRecord{Overview: "o"}
	for _, cmd := range commands {
		rec.Suggestions = append(rec.Suggestions, manifest.Suggestion{Member: p.M.Members[0].Name, Kind: "setup", Command: cmd, Reason: "why-" + cmd[:min(len(cmd), 9)]})
	}
	if _, err := c.RecordInit(p, rec); err != nil {
		t.Fatal(err)
	}
}

// setupOf returns the setup commands of a package's first member.
func setupOf(t *testing.T, c *core.Core, pkg string) manifest.Commands {
	t.Helper()
	p, err := c.Load(pkg)
	if err != nil {
		t.Fatal(err)
	}
	return p.M.Members[0].Setup
}

func TestReviewFromPackages(t *testing.T) {
	env := testutil.NewEnv(t)
	seedPackage(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	c := d.app.env.core
	d.key("v")
	d.has("Nothing to review")
	d.key("esc")
	suggest(t, c, "pay", "one", "two", "three")
	d.key("ctrl+r")
	d.has("3 to review; v")
	d.key("v")
	d.has("$ one", "why-two", "in each new worktree")
	d.key("a")
	d.key("e")
	d.typeText(" -q")
	d.key("enter")
	if got := setupOf(t, c, "pay"); !reflect.DeepEqual(got, manifest.Commands{"one", "two -q"}) {
		t.Fatalf("setup = %v", got)
	}
	d.key("x")
	if got := setupOf(t, c, "pay"); len(got) != 2 {
		t.Errorf("a dismissed command was added: %v", got)
	}
	if _, ok := d.app.top().(*packagesModel); !ok {
		t.Errorf("finishing should go back, not stay on %T", d.app.top())
	}
}

func TestReviewAloneQuits(t *testing.T) {
	env := testutil.NewEnv(t)
	seedPackage(t, env, "pay", env.NewSource(t, "alpha"))
	c := newDriver(t, env, "").app.env.core
	for key, want := range map[string]int{"q": 0, "esc": 0} {
		suggest(t, c, "pay", "one", "two")
		d := &driver{t: t, app: newReviewApp(c, "pay", "recorded")}
		d.send(tea.WindowSizeMsg{Width: 100, Height: 30})
		d.run(d.app.Init())
		d.has("recorded", "$ two")
		d.key(key)
		if !d.quit || len(setupOf(t, c, "pay")) != want {
			t.Errorf("%s: quit = %v, setup = %v", key, d.quit, setupOf(t, c, "pay"))
		}
	}
	d := &driver{t: t, app: newReviewApp(c, "pay", "")}
	d.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	d.run(d.app.Init())
	d.has("$ two")
	d.key("A")
	if !d.quit || len(setupOf(t, c, "pay")) != 2 {
		t.Errorf("accept all: quit = %v, setup = %v", d.quit, setupOf(t, c, "pay"))
	}
}

func TestReviewShowsWholeCommandsAndAcceptsOnlyWhatShows(t *testing.T) {
	env := testutil.NewEnv(t)
	seedPackage(t, env, "pay", env.NewSource(t, "alpha"))
	c := newDriver(t, env, "").app.env.core
	var long []string
	for _, r := range "abcdef" {
		long = append(long, strings.Repeat(string(r), 400)+"END"+string(r))
	}
	suggest(t, c, "pay", long...)
	d := &driver{t: t, app: newReviewApp(c, "pay", "")}
	d.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	d.run(d.app.Init())
	d.has("ENDa")
	d.key("A")
	if len(setupOf(t, c, "pay")) != 0 {
		t.Error("accepted commands that were not on the screen")
	}
	d.has("one at a time")
	for range long {
		d.key("down")
	}
	d.has("ENDf")
}

func TestInitializeOpensTheReviewAndStopsWhenStopped(t *testing.T) {
	c := handoffEnv(t, false)
	c.Cfg.Loaded = true
	app := NewApp(c, "")
	app.openReview("p")
	if len(app.stack) != 1 {
		t.Error("nothing to review")
	}
	suggest(t, c, "p", "one")
	app.openReview("p")
	if _, ok := app.top().(*reviewModel); !ok {
		t.Errorf("top = %T", app.top())
	}
	stubInit(t, func(*harness.Launch) error { return errors.Join(harness.ErrStopped, &harness.ExitError{Code: 1}) })
	if _, again, err := handoff(c, &LaunchRequest{Package: "p", Init: true}); again || err == nil {
		t.Errorf("a stopped vmono must not reopen: %v %v", again, err)
	}
}
