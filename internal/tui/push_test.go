package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/testutil"
)

// finishedPay seeds package "pay" over the named sources, commits one file in
// every member of its session "work", and finishes it. Each source's remote is
// the bare repo testutil made for it.
func finishedPay(t *testing.T, env *testutil.Env, names ...string) map[string]string {
	t.Helper()
	srcs := map[string]string{}
	var list []string
	for _, n := range names {
		srcs[n] = env.NewSource(t, n)
		list = append(list, srcs[n])
	}
	seed(t, env, "pay", list...)
	for _, n := range names {
		commitInSession(t, env, "pay", "work", n, n+".txt")
	}
	c := coreForTest(t)
	s, err := c.ResolveSession("pay/work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Finish(s, core.FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	return srcs
}

// remoteHead returns the commit the named bare remote has on main.
func remoteHead(t *testing.T, env *testutil.Env, name string) string {
	t.Helper()
	return testutil.Git(t, filepath.Join(env.Root, "remotes", name+".git"), "rev-parse", "refs/heads/main")
}

func TestPushFromTheSessionsViewPublishesTheFinishedSession(t *testing.T) {
	env := testutil.NewEnv(t)
	srcs := finishedPay(t, env, "alpha")
	before := remoteHead(t, env, "alpha")
	d := newDriver(t, env, "")

	d.key("right")
	d.has("Sessions · pay (1)", "finished")
	d.eventually(func() bool { return containsView(d, "1 commit in 1 of 1 members") }, "the preview to say what is left to push")
	d.key("P")
	d.has("push session", "pay/work", "alpha", "1 commit of main → origin/main", "never forced", "last fetch")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if got, want := remoteHead(t, env, "alpha"), testutil.Git(t, srcs["alpha"], "rev-parse", "main"); got != want || got == before {
		t.Errorf("origin main = %s, want the landed tip %s (was %s)", got, want, before)
	}
	d.key("enter")
	d.has("Sessions · pay (1)", "finished")
	d.lacks("push session")
	d.eventually(func() bool { return containsView(d, "nothing, all on the remotes") }, "the preview to show nothing left to push")

	d.key("P")
	d.has("push session", "nothing to push", "already on its remote")
	d.key("y") // nothing to confirm
	d.has("push session")
	d.key("esc")
	d.lacks("push session")
}

func TestPushRefusesASessionThatIsNotFinished(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	before := remoteHead(t, env, "alpha")
	d := newDriver(t, env, "")

	d.key("right", "P")
	d.has("finish the session before pushing")
	d.lacks("push session")
	if remoteHead(t, env, "alpha") != before {
		t.Error("an unfinished session must not push")
	}
}

func TestPushFromTheMembersViewCanTargetOneMember(t *testing.T) {
	env := testutil.NewEnv(t)
	finishedPay(t, env, "alpha", "beta")
	alphaBefore, betaBefore := remoteHead(t, env, "alpha"), remoteHead(t, env, "beta")
	d := newDriver(t, env, "")

	d.key("right", "m")
	d.has("Members · pay/work (2)", "alpha", "beta", "pushes to", "1 to push")
	d.key("p") // alpha is highlighted
	d.has("push session", "alpha", "1 commit of main → origin/main")
	// The dialog is drawn over the members table, so look at its own plan.
	if pc, ok := d.app.top().(*pushConfirm); !ok || len(pc.plan.Members) != 1 || pc.plan.Members[0].Name != "alpha" {
		t.Fatalf("the dialog should plan alpha alone: %+v", d.app.top())
	}
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if remoteHead(t, env, "alpha") == alphaBefore {
		t.Error("alpha should have been pushed")
	}
	if remoteHead(t, env, "beta") != betaBefore {
		t.Error("beta was not asked for and must not be pushed")
	}
	d.key("enter")
	d.has("Members · pay/work (2)")

	d.key("P")
	d.has("push session", "alpha", "nothing to push", "beta", "1 commit of main → origin/main")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if remoteHead(t, env, "beta") == betaBefore {
		t.Error("push all should have pushed beta")
	}
}

func TestPushShowsABlockedMemberAndTheRestStillGo(t *testing.T) {
	env := testutil.NewEnv(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	commitInSession(t, env, "pay", "work", "beta", "two.txt")
	other := filepath.Join(env.Root, "other")
	testutil.Git(t, env.Root, "clone", "--quiet", filepath.Join(env.Root, "remotes", "alpha.git"), other)
	testutil.WriteFile(t, filepath.Join(other, "theirs.txt"), "x")
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "main")
	testutil.Git(t, a, "fetch", "--quiet", "origin")
	c := coreForTest(t)
	s, err := c.ResolveSession("pay/work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Finish(s, core.FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	alphaBefore, betaBefore := remoteHead(t, env, "alpha"), remoteHead(t, env, "beta")
	d := newDriver(t, env, "")

	d.key("right", "P")
	d.has("push session", "blocked:", "diverged", "Blocked members", "beta", "1 commit of main → origin/main")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "diverged") && containsView(d, "go back") }, "the blocked member to be reported")
	if remoteHead(t, env, "beta") == betaBefore {
		t.Error("beta is independent of alpha and should have been pushed")
	}
	if remoteHead(t, env, "alpha") != alphaBefore {
		t.Error("a diverged branch must never be forced")
	}
}
