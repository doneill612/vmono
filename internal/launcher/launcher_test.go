package launcher

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
)

// fakeTmux records every tmux command and answers list-sessions with
// sessions, new-session with newID and display-message with here.
type fakeTmux struct {
	sessions string
	newID    string
	here     string
	calls    [][]string
	attached string
	replace  bool
}

// install swaps the tmux seams for the duration of a test.
func (f *fakeTmux) install(t *testing.T) {
	t.Helper()
	origRun, origAttach, origLook := tmuxRun, tmuxAttach, lookPath
	tmuxRun = func(args ...string) (string, error) {
		f.calls = append(f.calls, args)
		switch args[0] {
		case "list-sessions":
			return f.sessions, nil
		case "display-message":
			return f.here + "\n", nil
		case "new-session":
			return f.newID + "\n", nil
		}
		return "", nil
	}
	tmuxAttach = func(target string, replace bool) error {
		f.attached, f.replace = target, replace
		return nil
	}
	lookPath = func(string) (string, error) { return "/usr/bin/tmux", nil }
	t.Cleanup(func() { tmuxRun, tmuxAttach, lookPath = origRun, origAttach, origLook })
}

// commands returns the subcommand of every recorded call.
func (f *fakeTmux) commands() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c[0]
	}
	return out
}

// call returns the first recorded call to the subcommand, or nil.
func (f *fakeTmux) call(sub string) []string {
	for _, c := range f.calls {
		if c[0] == sub {
			return c
		}
	}
	return nil
}

func testLaunch() *harness.Launch {
	return &harness.Launch{
		Name:      "claude",
		Dir:       "/pk/pay.ments/work",
		Path:      "/bin/claude",
		Args:      []string{"--resume"},
		Package:   "pay.ments",
		Session:   "work",
		Overrides: map[string]string{"VMONO_SESSION": "work", "VMONO_PACKAGE": "pay.ments"},
	}
}

func TestResolve(t *testing.T) {
	origLook := lookPath
	t.Cleanup(func() { lookPath = origLook })
	have := func(string) (string, error) { return "/usr/bin/tmux", nil }
	missing := func(string) (string, error) { return "", exec.ErrNotFound }

	cases := []struct {
		launcher, tmuxEnv string
		look              func(string) (string, error)
		want              string
		wantErr           bool
	}{
		{"", "", have, "direct", false},
		{"direct", "/tmp/tmux-1/default,1,0", have, "direct", false},
		{"auto", "", have, "direct", false},
		{"auto", "/tmp/tmux-1/default,1,0", have, "tmux", false},
		{"auto", "/tmp/tmux-1/default,1,0", missing, "direct", false},
		{"tmux", "", have, "tmux", false},
		{"tmux", "", missing, "", true},
	}
	for _, c := range cases {
		t.Setenv("TMUX", c.tmuxEnv)
		lookPath = c.look
		got, err := Resolve(config.Config{Launcher: c.launcher})
		if c.wantErr {
			if err == nil {
				t.Errorf("%+v: expected an error", c)
			}
			continue
		}
		if err != nil || got.Name() != c.want {
			t.Errorf("launcher=%q TMUX=%q: got %v, %v; want %s", c.launcher, c.tmuxEnv, got, err, c.want)
		}
	}
}

func TestDirectRunsInPlace(t *testing.T) {
	var got *harness.Launch
	var replace bool
	run := func(l *harness.Launch, r bool) error { got, replace = l, r; return errors.New("boom") }
	l := testLaunch()
	res, err := Direct{}.Launch(l, Opts{Replace: true, RunDirect: run})
	if res != HandedOff || err == nil || err.Error() != "boom" || got != l || !replace {
		t.Errorf("direct = %v, %v, launched %v replace=%v", res, err, got, replace)
	}
}

// TestTmuxCreatesTagsAndSwitches pins the commands for a session that has no
// tmux session yet, from inside tmux: create detached at the session root
// with vmono's env, tag it, then switch the client to it by id.
func TestTmuxCreatesTagsAndSwitches(t *testing.T) {
	f := &fakeTmux{sessions: "$0\t\n$1\tother/work\n", newID: "$7"}
	f.install(t)
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	t.Setenv("PATH", "/my/bin")

	res, err := Tmux{}.Launch(testLaunch(), Opts{Replace: true})
	if err != nil || res != Elsewhere {
		t.Fatalf("launch = %v, %v", res, err)
	}
	if want := []string{"list-sessions", "new-session", "set-option", "switch-client"}; !slices.Equal(f.commands(), want) {
		t.Fatalf("commands = %v, want %v", f.commands(), want)
	}
	want := []string{
		"new-session", "-d", "-P", "-F", "#{session_id}", "-s", "pay_ments/work", "-c", "/pk/pay.ments/work",
		"-e", "PATH=/my/bin", "-e", "VMONO_PACKAGE=pay.ments", "-e", "VMONO_SESSION=work",
		"/bin/claude", "--resume",
	}
	if got := f.call("new-session"); !slices.Equal(got, want) {
		t.Errorf("new-session =\n  %q\nwant\n  %q", got, want)
	}
	if got := f.call("set-option"); !slices.Equal(got, []string{"set-option", "-t", "$7", TagOption, "pay.ments/work"}) {
		t.Errorf("set-option = %q", got)
	}
	if got := f.call("switch-client"); !slices.Equal(got, []string{"switch-client", "-t", "$7"}) {
		t.Errorf("switch-client = %q", got)
	}
	if f.attached != "" {
		t.Error("must not attach from inside tmux")
	}
}

// TestTmuxReusesTaggedSession pins that a vmono session that already has a
// tmux session goes straight to it, found by tag even under another name,
// and never starts a second harness.
func TestTmuxReusesTaggedSession(t *testing.T) {
	f := &fakeTmux{sessions: "$2\tpay.ments/work\n"}
	f.install(t)
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")

	if res, err := (Tmux{}).Launch(testLaunch(), Opts{}); err != nil || res != Elsewhere {
		t.Fatalf("launch = %v, %v", res, err)
	}
	if want := []string{"list-sessions", "switch-client"}; !slices.Equal(f.commands(), want) {
		t.Fatalf("commands = %v, want %v", f.commands(), want)
	}
	if got := f.call("switch-client"); got[2] != "$2" {
		t.Errorf("switched to %q, want $2", got[2])
	}
}

// TestTmuxAttachesFromOutside pins that outside tmux the terminal is handed
// to `tmux attach`, honoring the caller's choice to replace vmono.
func TestTmuxAttachesFromOutside(t *testing.T) {
	f := &fakeTmux{newID: "$4"}
	f.install(t)
	t.Setenv("TMUX", "")

	res, err := Tmux{}.Launch(testLaunch(), Opts{Replace: true})
	if err != nil || res != HandedOff {
		t.Fatalf("launch = %v, %v", res, err)
	}
	if f.attached != "$4" || !f.replace {
		t.Errorf("attached %q replace=%v, want $4 and replace", f.attached, f.replace)
	}
	if f.call("switch-client") != nil {
		t.Error("must not switch-client from outside tmux")
	}
}

// TestTmuxReportsAHarnessThatExitsAtOnce pins the hint given when the new
// session is already gone by the time vmono tags it.
func TestTmuxReportsAHarnessThatExitsAtOnce(t *testing.T) {
	f := &fakeTmux{newID: "$5"}
	f.install(t)
	inner := tmuxRun
	tmuxRun = func(args ...string) (string, error) {
		if args[0] == "set-option" {
			return "", errors.New("tmux set-option: can't find session: $5")
		}
		return inner(args...)
	}
	_, err := Tmux{}.Launch(testLaunch(), Opts{})
	if err == nil || !strings.Contains(err.Error(), "VMONO_LAUNCHER=direct vmono open pay.ments/work") {
		t.Errorf("err = %v", err)
	}
}

// TestTmuxNoSwitchLeavesTheUserInPlace pins that with switching off the tmux
// session is still created and tagged, but the client is neither switched
// nor attached: inside tmux the user is told through the status line, and
// either way the result lets a TUI quit.
func TestTmuxNoSwitchLeavesTheUserInPlace(t *testing.T) {
	for _, inside := range []bool{true, false} {
		f := &fakeTmux{newID: "$7"}
		f.install(t)
		env := ""
		if inside {
			env = "/tmp/tmux-1/default,1,0"
		}
		t.Setenv("TMUX", env)

		res, err := Tmux{NoSwitch: true}.Launch(testLaunch(), Opts{Replace: true})
		if err != nil || res != Elsewhere {
			t.Fatalf("inside=%v: launch = %v, %v", inside, res, err)
		}
		if f.call("switch-client") != nil || f.attached != "" {
			t.Errorf("inside=%v: must neither switch nor attach (calls %v, attached %q)", inside, f.commands(), f.attached)
		}
		if f.call("set-option") == nil {
			t.Errorf("inside=%v: the session must still be created and tagged: %v", inside, f.commands())
		}
		msg := f.call("display-message")
		if inside {
			if msg == nil || !strings.Contains(msg[1], `"pay_ments/work"`) {
				t.Errorf("inside tmux the status line should name the session: %q", msg)
			}
		} else if msg != nil {
			t.Errorf("display-message needs a client; outside tmux there is none: %q", msg)
		}
	}
}

// TestResolveCarriesTheSwitchSetting pins that tmux_switch = false reaches
// the launcher, for both an explicit tmux launcher and auto.
func TestResolveCarriesTheSwitchSetting(t *testing.T) {
	origLook := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/tmux", nil }
	t.Cleanup(func() { lookPath = origLook })
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	for _, name := range []string{config.LauncherTmux, config.LauncherAuto} {
		for _, sw := range []bool{true, false} {
			got, err := Resolve(config.Config{Launcher: name, TmuxSwitch: sw})
			tm, ok := got.(Tmux)
			if err != nil || !ok || tm.NoSwitch == sw {
				t.Errorf("launcher=%s tmux_switch=%v: got %#v, %v", name, sw, got, err)
			}
		}
	}
}

func TestTmuxSessionName(t *testing.T) {
	for in, want := range map[string]string{
		"payments/work": "payments/work",
		"v1.2/fix:it":   "v1_2/fix_it",
	} {
		if got := TmuxSessionName(in); got != want {
			t.Errorf("TmuxSessionName(%q) = %q, want %q", in, got, want)
		}
	}
}
