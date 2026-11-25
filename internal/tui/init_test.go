package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/launcher"
	"github.com/doneill612/vmono/internal/testutil"
)

// stubInit swaps the harness that initializes a package.
func stubInit(t *testing.T, fn func(l *harness.Launch) error) {
	t.Helper()
	orig := runInitHarness
	runInitHarness = func(l *harness.Launch, _ bool) error { return fn(l) }
	t.Cleanup(func() { runInitHarness = orig })
}

func TestInitializeFromPackages(t *testing.T) {
	c := handoffEnv(t, false)
	c.Cfg.Loaded = true
	d := newDriverFor(t, c, "")
	d.has("none; i initializes")
	d.key("i")
	d.has("initialize", "record an overview")
	d.key("enter")
	req := d.app.Launch()
	if req == nil || !req.Init || req.Package != "p" || req.Harness != "fake" {
		t.Fatalf("request = %+v", req)
	}

	stubRun(t, func(*harness.Launch, bool) error { t.Fatal("must not open a session"); return nil })
	stubInit(t, func(l *harness.Launch) error {
		p, err := c.Load("p")
		if err != nil {
			return err
		}
		_, err = c.RecordInit(p, core.InitRecord{Overview: "how it fits"})
		return err
	})
	msg, again, err := handoff(c, req)
	if err != nil || !again || msg != "recorded an overview of p" {
		t.Fatalf("handoff = %q %v %v", msg, again, err)
	}

	d = newDriverFor(t, c, msg)
	d.has("recorded an overview of p", "overview")
	d.key("d")
	d.has("how it fits")
}

func TestInitializeReportsFailuresAndQuitsForAMultiplexer(t *testing.T) {
	c := handoffEnv(t, false)
	stubInit(t, func(*harness.Launch) error { return &harness.ExitError{Code: 2} })
	if msg, again, err := handoff(c, &LaunchRequest{Package: "p", Init: true}); err != nil || !again || !strings.Contains(msg, "nothing was recorded") {
		t.Errorf("a harness's own exit is not a failure: %q %v %v", msg, again, err)
	}
	stubInit(t, func(*harness.Launch) error { return errors.New("could not start") })
	if msg, again, _ := handoff(c, &LaunchRequest{Package: "p", Init: true}); !again || !strings.Contains(msg, "could not start") {
		t.Errorf("failure = %q %v", msg, again)
	}
	if msg, again, _ := handoff(c, &LaunchRequest{Package: "ghost", Init: true}); !again || msg == "" {
		t.Errorf("unknown package = %q %v", msg, again)
	}
	f := &fakeLauncher{res: launcher.Elsewhere}
	stubLauncher(t, f)
	if msg, again, err := handoff(c, &LaunchRequest{Package: "p", Init: true}); !f.called || again || msg != "" || err != nil {
		t.Errorf("elsewhere should quit: %q %v %v", msg, again, err)
	}
}

func TestPreviewShowsAnOutdatedOverview(t *testing.T) {
	env := testutil.NewEnv(t)
	seedPackage(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	c := d.app.env.core
	p, err := c.Load("pay")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordInit(p, core.InitRecord{Overview: "o"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMember(p, core.AddOptions{Source: env.NewSource(t, "beta")}); err != nil {
		t.Fatal(err)
	}
	d.key("ctrl+r")
	d.has("out of date: +beta")
	d.key("d")
	d.has("out of date: +beta")
	// Saving the form must not lose what init recorded.
	d.key("esc", "e", "ctrl+s")
	if p, _ = c.Load("pay"); p.M.Overview() != "o" {
		t.Error("editing lost the overview")
	}
}
