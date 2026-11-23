package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestPackageWithNoSessionsOpensThePromptOnO(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seedPackage(t, env, "bare", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.has("bare", "0") // zero sessions
	d.key("o")
	d.has("new session", "package bare", "no sessions yet")
	d.typeText("first")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.has("bare/first")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "bare", "first", "alpha", "README.md")); err != nil {
		t.Error("the session should have a worktree of each member")
	}
	d.key("enter")
	if l := d.app.Launch(); !d.quit || l == nil || l.Package != "bare" || l.Session != "first" {
		t.Fatalf("launch = %+v", l)
	}
}

// TestLaunchPickerBackdropShowsTheNewSession guards the view drawn behind the
// "Launch now?" dialog: it must list the session that was just created, not
// the empty list loaded before the create ran.
func TestLaunchPickerBackdropShowsTheNewSession(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seedPackage(t, env, "bare", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter") // open the package's empty sessions view
	d.has("No sessions yet")
	d.key("n")
	d.typeText("first")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.eventually(func() bool { return containsView(d, "clean") }, "backdrop to list the new session")
	d.lacks("No sessions yet")
}

func TestSessionPromptValidatesAndOverridesBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter", "n")
	d.has("new session", "vmono/pay/<session>")
	d.key("enter") // empty name
	d.has("invalid name")
	d.typeText("Bad Name")
	d.key("enter")
	d.has("invalid name")
	for range "Bad Name" {
		d.key("backspace")
	}
	d.typeText("work") // already taken by the seeded session
	d.key("enter")
	d.has("already has a session named \"work\"")
	for range "work" {
		d.key("backspace")
	}
	d.typeText("hotfix")
	d.has("vmono/pay/hotfix") // the placeholder follows the name
	d.key("tab")
	d.typeText("fix/PLAT-7")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	br := testutil.Git(t, filepath.Join(env.PackageRoot, "pay", "hotfix", "alpha"), "branch", "--show-current")
	if br != "fix/PLAT-7" {
		t.Errorf("branch = %q, want the override", br)
	}
	d.key("esc")
	d.has("sessions(pay)[2]", "hotfix", "work")
}

func TestPromptEscCancelsWithoutCreating(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("enter", "n")
	d.typeText("nope")
	d.key("esc")
	d.has("sessions(pay)[1]")
	d.lacks("new session")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "nope")); err == nil {
		t.Error("cancelling must not create anything")
	}
}

func TestOpenKeyLaunchesTheMostRecentlyOpenedSession(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	// Start a second session, then launch it so it becomes the latest.
	d.key("enter", "n")
	d.typeText("second")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.key("enter")
	if l := d.app.Launch(); l == nil || l.Session != "second" {
		t.Fatalf("launch = %+v", l)
	}
	// Prepare is what records the launch; run it as the real handoff does.
	stubRun(t, func(*harness.Launch, bool) error { return nil })
	if _, _, err := handoff(d.app.env.core, d.app.Launch()); err != nil {
		t.Fatal(err)
	}

	d2 := newDriver(t, env, "")
	d2.key("o") // from Home: the latest session, without asking
	if l := d2.app.Launch(); !d2.quit || l == nil || l.Session != "second" {
		t.Fatalf("o should launch the most recently opened session: %+v", l)
	}
	d3 := newDriver(t, env, "")
	d3.key("l")
	d3.has("Launch with", "pay/second")
}

func TestDeleteSessionKeepsThePackageAndSiblings(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("enter", "n")
	d.typeText("extra")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.key("esc")
	d.has("sessions(pay)[2]")

	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "pay", "extra", "alpha", "wip.txt"), "x")
	d.key("j") // sorted by opened/name: move onto "extra"
	if sel, _ := d.app.top().(*sessionsModel).selected(); sel.Name != "extra" {
		d.key("k")
	}
	d.key("ctrl+d")
	d.has("Delete session", "extra/alpha", "untracked", "Type the session name")
	d.typeText("extra")
	d.key("enter")
	d.eventually(func() bool {
		_, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "extra"))
		return os.IsNotExist(err)
	}, "session removal")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "work", "alpha", "README.md")); err != nil {
		t.Error("the other session must survive")
	}
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "pay", ".vmono", "manifest.toml")); err != nil {
		t.Error("the package must survive")
	}
}

func TestLegacyPackageIsMarkedAndBlocked(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "new", env.NewSource(t, "alpha"))
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "old", ".vmono", "manifest.toml"),
		"schema = 1\nname = \"old\"\nbranch = \"vmono/old\"\n")
	d := newDriver(t, env, "")

	d.has("old", "migrate", "new")
	d.key("/")
	d.typeText("old")
	d.key("enter")
	for _, k := range []string{"enter", "o", "e", "s"} {
		d.key(k)
		d.has("vmono doctor --fix")
		if d.quit {
			t.Fatalf("%q on a legacy package must not launch", k)
		}
	}
}

func TestHandoffTargetsASessionAndExplainsAmbiguity(t *testing.T) {
	c := handoffEnv(t, false)
	p, err := c.Load("p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSession(p, "other", coreSessionOptions()); err != nil {
		t.Fatal(err)
	}
	var got *harness.Launch
	stubRun(t, func(l *harness.Launch, _ bool) error { got = l; return nil })

	msg, again, err := handoff(c, &LaunchRequest{Package: "p", Session: "other"})
	if again || msg != "" || err != nil || got == nil || filepath.Base(got.Dir) != "other" {
		t.Fatalf("launch into a named session: %q %v %v %+v", msg, again, err, got)
	}
	got = nil
	msg, again, _ = handoff(c, &LaunchRequest{Package: "p"})
	if !again || got != nil || !strings.Contains(msg, "p/NAME") || !strings.Contains(msg, "other, work") {
		t.Errorf("a bare package with two sessions should explain itself: %q again=%v", msg, again)
	}
	msg, again, _ = handoff(c, &LaunchRequest{Package: "p", Session: "ghost"})
	if !again || got != nil || msg == "" {
		t.Errorf("unknown session: %q again=%v", msg, again)
	}
}

// coreSessionOptions is a default-branch session request.
func coreSessionOptions() core.SessionOptions { return core.SessionOptions{} }

func TestEnterOnASessionOpensTheActionMenu(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter") // into the sessions view
	d.key("enter") // on a session: the menu, not a launch
	if d.quit || d.app.Launch() != nil {
		t.Fatal("enter on a session must not launch directly")
	}
	d.has("Launch", "Launch with…", "Members", "Describe", "Sync", "Fetch", "Copy path", "Finish", "Delete")

	d.key("esc")
	d.lacks("enter select")

	d.key("enter", "enter") // menu, then its first entry
	if l := d.app.Launch(); !d.quit || l == nil || l.Package != "pay" || l.Session != "work" {
		t.Fatalf("launch = %+v", l)
	}
}

func TestSessionMenuRunsTheChosenAction(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter", "enter", "down", "enter") // Launch with…
	d.has("pay/work", "not now")
	d.key("esc")
	d.lacks("not now")

	d.key("enter", "m") // an action's own key works inside the menu
	d.lacks("enter select")
	d.has("members")
}
