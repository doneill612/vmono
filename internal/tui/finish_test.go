package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/testutil"
)

// commitInSession commits a new file in a session's member worktree and
// returns that worktree.
func commitInSession(t *testing.T, env *testutil.Env, pkg, session, member, name string) string {
	t.Helper()
	wt := filepath.Join(env.PackageRoot, pkg, session, member)
	testutil.WriteFile(t, filepath.Join(wt, name), name+"\n")
	testutil.Git(t, wt, "add", "-A")
	testutil.Git(t, wt, "commit", "--quiet", "-m", "add "+name)
	return wt
}

// coreForTest builds a Core over the isolated test environment.
func coreForTest(t *testing.T) *core.Core {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestFinishLandsTheWorkAndKeepsTheSession(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	wt := commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	d := newDriver(t, env, "")

	d.key("enter") // the package's sessions
	d.has("sessions(pay)[1]", "clean")
	d.key("F")
	d.has("finish session", "pay/work", "alpha", "1 commit → main (fast-forward)", "Nothing is pushed", "[ ] also remove")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "finish to complete")
	if got, want := testutil.Git(t, a, "rev-parse", "main"), testutil.Git(t, wt, "rev-parse", "HEAD"); got != want {
		t.Errorf("main = %s, want the session tip %s", got, want)
	}
	d.key("enter")
	d.has("sessions(pay)[1]", "work", "finished")
	if _, err := os.Stat(wt); err != nil {
		t.Error("finishing must leave the worktree in place")
	}
}

func TestFinishDialogRefusesWhileAMemberIsBlocked(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	wt := commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	testutil.WriteFile(t, filepath.Join(wt, "forgotten.txt"), "x")
	main := testutil.Git(t, a, "rev-parse", "main")
	d := newDriver(t, env, "")

	d.key("enter", "F")
	d.has("blocked: uncommitted changes", "1 untracked", "Nothing lands until every member above can")
	d.key("y", "c")
	d.has("finish session")
	d.lacks("working…")
	if testutil.Git(t, a, "rev-parse", "main") != main {
		t.Error("a blocked finish must not move main")
	}
	d.key("esc")
	d.has("sessions(pay)[1]")
	d.lacks("finish session")
}

func TestFinishConflictOffersTheHarness(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	wt := filepath.Join(env.PackageRoot, "pay", "work", "alpha")
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "session version\n")
	testutil.Git(t, wt, "commit", "--quiet", "-am", "session edit")
	testutil.WriteFile(t, filepath.Join(a, "README.md"), "main version\n")
	testutil.Git(t, a, "commit", "--quiet", "-am", "main edit")
	main := testutil.Git(t, a, "rev-parse", "main")
	d := newDriver(t, env, "")

	d.key("enter", "F")
	d.has("1 commit → main") // a conflict only shows once the rebase runs
	d.key("y")
	d.eventually(func() bool { return containsView(d, "stopped on conflicts") }, "the conflict to be reported")
	d.has("README.md", "launch a harness in the session to fix it")
	if testutil.Git(t, a, "rev-parse", "main") != main {
		t.Error("main must not move while a member is conflicted")
	}
	d.key("l")
	if l := d.app.Launch(); !d.quit || l == nil || l.Package != "pay" || l.Session != "work" {
		t.Fatalf("launch = %+v", l)
	}
}

func TestFinishCanRemoveTheSessionAfterward(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	d := newDriver(t, env, "")

	d.key("enter", "F", "c")
	d.has("[x] also remove")
	d.key("y")
	d.eventually(func() bool {
		_, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "work"))
		return os.IsNotExist(err)
	}, "the session to be removed")
	if _, err := os.Stat(filepath.Join(a, "one.txt")); err != nil {
		t.Errorf("the landed work must stay in the source: %v", err)
	}
}

func TestFinishedSessionsSortBelowTheRest(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	c := coreForTest(t)
	p, err := c.Load("pay")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSession(p, "zeta", core.SessionOptions{}); err != nil {
		t.Fatal(err)
	}
	s, err := c.ResolveSession("pay/work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Finish(s, core.FinishOptions{}); err != nil {
		t.Fatal(err)
	}

	d := newDriver(t, env, "")
	d.key("enter")
	d.has("sessions(pay)[2]", "finished just now")
	rows := d.app.top().(*sessionsModel).tb.rows
	if len(rows) != 2 || rows[0].id != "zeta" || rows[1].id != "work" {
		t.Errorf("a finished session belongs below the rest, got %+v", rows)
	}
}

func TestSessionPromptTakesALandingBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	testutil.Git(t, a, "branch", "feature")
	d := newDriver(t, env, "")

	d.key("enter", "n")
	d.has("into:", "each member's base branch")
	d.typeText("stacked")
	d.key("tab", "tab") // name -> branch -> into
	d.typeText("feature")
	d.key("shift+tab", "shift+tab") // back to name
	d.key("tab", "tab")             // forward to into again, which must still hold its text
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")

	data, err := os.ReadFile(filepath.Join(env.PackageRoot, "pay", "stacked", ".vmono", "session.toml"))
	if err != nil || !containsText(string(data), `into    = "feature"`) {
		t.Errorf("session.toml = %q, %v", data, err)
	}
	want := testutil.Git(t, a, "rev-parse", "feature")
	if got := testutil.Git(t, filepath.Join(env.PackageRoot, "pay", "stacked", "alpha"), "rev-parse", "HEAD"); got != want {
		t.Errorf("the session should start at feature: %s, want %s", got, want)
	}
}

// containsText reports whether s contains sub.
func containsText(s, sub string) bool { return indexOf(s, sub) >= 0 }
