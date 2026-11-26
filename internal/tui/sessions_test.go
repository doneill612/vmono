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

	d.key("right") // open the package's empty sessions view
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

	d.key("right", "n")
	d.has("new session", "<session>")
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
	d.has("branch:   hotfix") // the placeholder follows the name
	d.key("tab", "tab")
	d.typeText("fix/PLAT-7")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	br := testutil.Git(t, filepath.Join(env.PackageRoot, "pay", "hotfix", "alpha"), "branch", "--show-current")
	if br != "fix/PLAT-7" {
		t.Errorf("branch = %q, want the override", br)
	}
	d.key("esc")
	d.has("Sessions · pay (2)", "hotfix", "work")
}

func TestPromptEscCancelsWithoutCreating(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("right", "n")
	d.typeText("nope")
	d.key("esc")
	d.has("Sessions · pay (1)")
	d.lacks("<session>")
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
	d.key("right", "n")
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
	d.key("right", "n")
	d.typeText("extra")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.key("esc")
	d.has("Sessions · pay (2)")

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

	d.key("right") // into the sessions view
	d.key("enter") // on a session: the menu, not a launch
	if d.quit || d.app.Launch() != nil {
		t.Fatal("enter on a session must not launch directly")
	}
	d.has("Launch", "Launch with…", "Members", "Describe", "Sync", "Fetch", "Copy path", "Update", "Push", "Delete")

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

	d.key("right", "enter", "down", "enter") // Launch with…
	d.has("pay/work", "not now")
	d.key("esc")
	d.lacks("not now")

	d.key("enter", "m") // an action's own key works inside the menu
	d.lacks("enter select")
	d.has("Members")
}

func TestEnterOnAPackageOpensTheActionMenu(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter")
	d.has("Sessions", "Launch latest", "Launch with…", "New session", "Describe", "Edit", "Sync all", "Fetch", "Copy path", "Delete")
	d.lacks("Sessions · pay")

	d.key("enter") // the first entry drills into the sessions
	d.has("Sessions · pay (1)")
	d.lacks("Launch latest")

	d.key("esc", "enter", "d") // an entry's own key works inside the menu
	d.has("Describe · pay")
}

// TestListsShowASpinnerNotACountUntilLoaded guards the pause after drilling
// into a package: until the load lands, the sessions and members views must
// show the animated loading text and no row count, rather than an empty list
// titled "(0)".
func TestListsShowASpinnerNotACountUntilLoaded(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")

	for _, tc := range []struct {
		name  string
		s     screen
		title string
	}{
		{"sessions", newSessions(d.app.env, "pay"), "Sessions"},
		{"members", newMembers(d.app.env, "pay", "main"), "Members"},
		{"packages", newPackages(d.app.env), "Packages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Title(); strings.Contains(got, "(") {
				t.Errorf("title %q shows a count before anything loaded", got)
			}
			view := tc.s.View(60, 10)
			if !strings.Contains(view, "loading…") {
				t.Errorf("view before load lacks the loading text:\n%s", view)
			}
			if !strings.Contains(view, newSpinner().Spinner.Frames[0]) {
				t.Errorf("view before load lacks the spinner:\n%s", view)
			}
		})
	}
}

func TestUpdateRunsFromTheSessionsViewAndReturnsToIt(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("right", "u")
	d.eventually(func() bool { return containsView(d, "done") }, "the update to finish")
	d.has("Update pay/work")
	d.key("enter")
	d.has("clean")
	d.lacks("Update pay/work")
}

// TestDeleteDialogSaysWhatBecomesOfTheBranch covers a branch that was
// squash-merged, which is kept until the dialog is told to delete it.
func TestDeleteDialogSaysWhatBecomesOfTheBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	srcs := seedWork(t, env, "alpha")
	a := srcs["alpha"]
	commitInSession(t, env, "pay", "work", "alpha", "more.txt")
	testutil.Git(t, a, "push", "--quiet", "-u", "origin", payBranch)
	d := newDriver(t, env, "")
	d.key("right", "ctrl+d")
	d.has("Nothing would be lost", "work is kept: 2 commits not merged, on origin/work")
	d.key("b")
	d.has("work is kept")
	d.key("esc")

	testutil.Git(t, a, "merge", "--squash", "--quiet", payBranch)
	testutil.Git(t, a, "commit", "--quiet", "-m", "squashed")
	testutil.Git(t, a, "push", "--quiet", "origin", "main")
	d.key("ctrl+r")
	d.eventually(func() bool { return containsView(d, "merged (squash)") }, "the list to say it is merged")
	d.has("everything is merged")
	d.key("P")
	d.has("already squash-merged into origin/main")
	d.key("esc", "ctrl+d")
	d.has("work is kept: merged (squash) into origin/main")
	d.key("b")
	d.has("work is deleted: merged (squash)")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the delete")
	if out := testutil.Git(t, a, "branch", "--list", payBranch); out != "" {
		t.Errorf("the merged branch should be gone: %q", out)
	}
}

func TestMergedSessionsSortBelowTheRest(t *testing.T) {
	env := testutil.NewEnv(t)
	srcs := seedWork(t, env, "alpha")
	c := coreForTest(t)
	p, err := c.Load("pay")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSession(p, "zeta", core.SessionOptions{}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, srcs["alpha"], "push", "--quiet", "origin", payBranch, payBranch+":main")
	testutil.Git(t, srcs["alpha"], "fetch", "--quiet")
	d := newDriver(t, env, "")
	d.key("right")
	d.has("Sessions · pay (2)", "merged (merge)")
	rows := d.app.top().(*sessionsModel).tb.rows
	if len(rows) != 2 || rows[0].id != "zeta" || rows[1].id != "work" {
		t.Errorf("a merged session belongs below the rest, got %+v", rows)
	}
}
