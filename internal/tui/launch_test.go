package tui

import (
	"errors"
	"runtime"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/launcher"
	"github.com/doneill612/vmono/internal/testutil"
)

// handoffEnv builds a Core with a package "p" and a harness "fake" (sh).
func handoffEnv(t *testing.T, returnToTUI bool) *core.Core {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	env := testutil.NewEnv(t)
	seed(t, env, "p", env.NewSource(t, "alpha"))
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Harness["fake"] = config.Harness{Command: "sh"}
	cfg.DefaultHarness = "fake"
	cfg.ReturnToTUI = returnToTUI
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// stubRun swaps runHarness for the duration of a test.
func stubRun(t *testing.T, fn func(*harness.Launch, bool) error) {
	t.Helper()
	orig := runHarness
	runHarness = fn
	t.Cleanup(func() { runHarness = orig })
}

func TestHandoffExecsWhenNotReturningToTUI(t *testing.T) {
	c := handoffEnv(t, false)
	var replace bool
	var got *harness.Launch
	stubRun(t, func(l *harness.Launch, r bool) error { got, replace = l, r; return errors.New("exec failed") })

	msg, again, err := handoff(c, &LaunchRequest{Package: "p"})
	if again || msg != "" || err == nil || err.Error() != "exec failed" {
		t.Fatalf("handoff = %q, %v, %v", msg, again, err)
	}
	if !replace || got == nil || got.Name != "fake" || got.Dir == "" {
		t.Errorf("should exec the resolved harness at the package root: %+v replace=%v", got, replace)
	}
}

func TestHandoffReturnsToTUIAsChild(t *testing.T) {
	c := handoffEnv(t, true)
	var replace = true
	stubRun(t, func(l *harness.Launch, r bool) error { replace = r; return &harness.ExitError{Code: 3} })

	msg, again, err := handoff(c, &LaunchRequest{Package: "p", Harness: "fake"})
	if !again || msg != "" || err != nil {
		t.Fatalf("a harness's own non-zero exit must not be a vmono failure: %q, %v, %v", msg, again, err)
	}
	if replace {
		t.Error("return_to_tui must run the harness as a child, not exec")
	}

	stubRun(t, func(*harness.Launch, bool) error { return errors.New("could not start") })
	if msg, again, _ = handoff(c, &LaunchRequest{Package: "p"}); !again || msg != "could not start" {
		t.Errorf("start failure should reopen Home with a message: %q, %v", msg, again)
	}
}

// fakeLauncher stands in for tmux in handoff tests.
type fakeLauncher struct {
	res     launcher.Result
	err     error
	replace bool
	called  bool
}

func (f *fakeLauncher) Name() string { return "fake" }

func (f *fakeLauncher) Launch(_ *harness.Launch, o launcher.Opts) (launcher.Result, error) {
	f.called, f.replace = true, o.Replace
	return f.res, f.err
}

// stubLauncher makes handoff use ln for the duration of a test.
func stubLauncher(t *testing.T, ln launcher.Launcher) {
	t.Helper()
	orig := resolveLauncher
	resolveLauncher = func(config.Config) (launcher.Launcher, error) { return ln, nil }
	t.Cleanup(func() { resolveLauncher = orig })
}

// TestHandoffWithAMultiplexer pins what the TUI does after a non-direct
// launch: quit once the client was switched elsewhere, even with
// return_to_tui; follow return_to_tui after an attach; and show a launch
// failure on Home rather than exit to a bare shell.
func TestHandoffWithAMultiplexer(t *testing.T) {
	stubRun(t, func(*harness.Launch, bool) error { t.Fatal("must not run in place"); return nil })

	c := handoffEnv(t, true)
	f := &fakeLauncher{res: launcher.Elsewhere}
	stubLauncher(t, f)
	if msg, again, err := handoff(c, &LaunchRequest{Package: "p"}); !f.called || again || msg != "" || err != nil {
		t.Errorf("switched elsewhere should quit: %q %v %v", msg, again, err)
	}

	f = &fakeLauncher{res: launcher.HandedOff}
	stubLauncher(t, f)
	if _, again, _ := handoff(c, &LaunchRequest{Package: "p"}); !again || f.replace {
		t.Errorf("attach with return_to_tui should run as a child and reopen: again=%v replace=%v", again, f.replace)
	}
	c.Cfg.ReturnToTUI = false
	if _, again, _ := handoff(c, &LaunchRequest{Package: "p"}); again || !f.replace {
		t.Errorf("attach without return_to_tui should replace vmono: again=%v replace=%v", again, f.replace)
	}

	stubLauncher(t, &fakeLauncher{err: errors.New("tmux new-session: duplicate session")})
	if msg, again, err := handoff(c, &LaunchRequest{Package: "p"}); !again || msg == "" || err != nil {
		t.Errorf("a launch failure should reopen Home with a message: %q %v %v", msg, again, err)
	}
}

func TestHandoffReportsPrepareFailuresOnHome(t *testing.T) {
	c := handoffEnv(t, false)
	stubRun(t, func(*harness.Launch, bool) error { t.Fatal("must not launch"); return nil })

	for _, req := range []*LaunchRequest{
		{Package: "ghost"},
		{Package: "p", Harness: "nope"},
	} {
		msg, again, err := handoff(c, req)
		if !again || msg == "" || err != nil {
			t.Errorf("%+v: msg=%q again=%v err=%v", req, msg, again, err)
		}
	}
	c.Cfg.Harness["gone"] = config.Harness{Command: "definitely-not-installed-xyz"}
	if msg, again, _ := handoff(c, &LaunchRequest{Package: "p", Harness: "gone"}); !again || msg == "" {
		t.Errorf("missing command should surface on Home: %q %v", msg, again)
	}
}
