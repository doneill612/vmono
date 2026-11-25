package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/testutil"
)

// payBranch is the branch of the session seedWork makes.
const payBranch = "vmono/pay/work"

// seedWork seeds package "pay" over the named sources, and commits one file in
// every member of its session "work". Each source's remote is the bare repo
// testutil made for it.
func seedWork(t *testing.T, env *testutil.Env, names ...string) map[string]string {
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
	return srcs
}

// remoteTip returns the commit the named bare remote has on the session's
// branch, or "" when it does not have the branch.
func remoteTip(t *testing.T, env *testutil.Env, name string) string {
	t.Helper()
	return testutil.Git(t, filepath.Join(env.Root, "remotes", name+".git"), "for-each-ref", "--format=%(objectname)", "refs/heads/"+payBranch)
}

func TestPushFromTheSessionsViewPublishesTheSession(t *testing.T) {
	env := testutil.NewEnv(t)
	srcs := seedWork(t, env, "alpha")
	d := newDriver(t, env, "")

	d.key("right", "P")
	d.has("push session", "pay/work", "alpha", "1 commit → origin/"+payBranch, "new", "never forced", "last fetch")
	if remoteTip(t, env, "alpha") != "" {
		t.Error("nothing is pushed until it is confirmed")
	}
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if got, want := remoteTip(t, env, "alpha"), testutil.Git(t, srcs["alpha"], "rev-parse", payBranch); got != want {
		t.Errorf("the remote has %q, want %s", got, want)
	}
	d.key("enter")
	d.has("Sessions · pay (1)")
	d.lacks("push session")

	d.key("P")
	d.has("push session", "nothing to push", "There is nothing to push")
	d.key("y") // nothing to confirm
	d.has("push session")
	d.key("esc")
	d.lacks("push session")
}

func TestPushFromTheMembersViewCanTargetOneMember(t *testing.T) {
	env := testutil.NewEnv(t)
	seedWork(t, env, "alpha", "beta")
	d := newDriver(t, env, "")

	d.key("right", "m")
	d.has("Members · pay/work (2)", "alpha", "beta")
	d.key("p") // alpha is highlighted
	d.has("push session", "alpha", "1 commit → origin/"+payBranch)
	// The dialog is drawn over the members table, so look at its own plan.
	if pc, ok := d.app.top().(*pushConfirm); !ok || len(pc.plan.Members) != 1 || pc.plan.Members[0].Name != "alpha" {
		t.Fatalf("the dialog should plan alpha alone: %+v", d.app.top())
	}
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if remoteTip(t, env, "alpha") == "" {
		t.Error("alpha should have been pushed")
	}
	if remoteTip(t, env, "beta") != "" {
		t.Error("beta was not asked for and must not be pushed")
	}
	d.key("enter")
	d.has("Members · pay/work (2)")

	d.key("P")
	d.has("push session", "alpha", "nothing to push", "beta", "1 commit → origin/"+payBranch)
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if remoteTip(t, env, "beta") == "" {
		t.Error("push all should have pushed beta")
	}
}

func TestPushShowsABlockedMemberAndTheRestStillGo(t *testing.T) {
	env := testutil.NewEnv(t)
	srcs := seedWork(t, env, "alpha", "beta")
	c := coreForTest(t)
	s, err := c.ResolveSession("pay/work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Push(s, core.PushOptions{Members: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(env.Root, "other")
	testutil.Git(t, env.Root, "clone", "--quiet", "--branch", payBranch, filepath.Join(env.Root, "remotes", "alpha.git"), other)
	testutil.WriteFile(t, filepath.Join(other, "theirs.txt"), "x")
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", payBranch)
	testutil.Git(t, srcs["alpha"], "fetch", "--quiet", "origin")
	commitInSession(t, env, "pay", "work", "alpha", "mine.txt")
	before := remoteTip(t, env, "alpha")
	d := newDriver(t, env, "")

	d.key("right", "P")
	d.has("push session", "blocked:", "diverged", "Blocked members", "beta", "1 commit → origin/"+payBranch)
	d.key("y")
	d.eventually(func() bool { return containsView(d, "diverged") && containsView(d, "go back") }, "the blocked member to be reported")
	if remoteTip(t, env, "beta") == "" {
		t.Error("beta is independent of alpha and should have been pushed")
	}
	if remoteTip(t, env, "alpha") != before {
		t.Error("a diverged branch must never be forced")
	}
}

func TestPushPointsOutUncommittedWork(t *testing.T) {
	env := testutil.NewEnv(t)
	seedWork(t, env, "alpha")
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "pay", "work", "alpha", "forgotten.txt"), "x")
	d := newDriver(t, env, "")

	d.key("right", "P")
	d.has("push session", "1 commit → origin/"+payBranch, "1 untracked")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "the push to complete")
	if remoteTip(t, env, "alpha") == "" {
		t.Error("what is committed should have been pushed")
	}
}
