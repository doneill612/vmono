package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestNewSessionWorksOnAPickedRemoteBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	tip := testutil.Git(t, a, "commit-tree", "-m", "theirs", "-p", "main", "main^{tree}")
	testutil.Git(t, a, "push", "--quiet", "origin", tip+":refs/heads/feat/x")
	testutil.Git(t, a, "update-ref", "-d", "refs/remotes/origin/feat/x") // only a fetch reveals it
	seedPackage(t, env, "pay", a, b)
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("work")
	d.key("ctrl+t")
	d.eventually(func() bool { return containsView(d, "feat/x") }, "the fetched branch")
	d.has("not in: beta", "none: start a new branch")
	d.typeText("feat")
	d.key("enter")
	d.has("new session", "feat/x  (alpha)")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	dir := filepath.Join(env.PackageRoot, "pay", "work")
	if got := testutil.Git(t, filepath.Join(dir, "alpha"), "rev-parse", "HEAD"); got != tip {
		t.Errorf("alpha starts at %s, want %s", got, tip)
	}
	if got := testutil.Git(t, filepath.Join(dir, "beta"), "rev-parse", "HEAD"); got != testutil.Git(t, b, "rev-parse", "main") {
		t.Errorf("beta should start from its base, got %s", got)
	}
	if got := sessionBranches(t, env, "work", "alpha", "beta"); got[0] != "feat/x" || got[1] != "feat/x" {
		t.Errorf("branches = %v", got)
	}
}

func TestRemotePickerWorksOffline(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/x")
	testutil.Git(t, a, "fetch", "--quiet")
	seedPackage(t, env, "pay", a)
	testutil.Git(t, a, "remote", "set-url", "origin", filepath.Join(env.Root, "gone.git"))
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("work")
	d.key("tab", "tab", "tab", "enter") // the track field opens the picker
	d.eventually(func() bool { return containsView(d, "couldn't fetch alpha") }, "the fetch notice")
	d.has("feat/x")
	d.typeText("feat")
	d.key("enter")
	d.has("feat/x  (alpha)")

	// Picking none restores the default.
	d.key("ctrl+t")
	d.key("enter")
	d.has("none, start a new branch")
	d.lacks("feat/x")
	d.key("tab", "tab", "enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
}

// TestMemberBranchWinsOverAPickedOne covers a member given a branch of its own
// after the pick: it starts a new one rather than tracking what was picked.
func TestMemberBranchWinsOverAPickedOne(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/x")
	seedPackage(t, env, "pay", a)
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("work")
	d.key("ctrl+t")
	d.eventually(func() bool { return containsView(d, "feat/x") }, "the branch")
	d.typeText("feat")
	d.key("enter", "tab", "tab", "tab", "tab", "tab")
	d.typeText("mine")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := sessionBranches(t, env, "work", "alpha"); got[0] != "mine" {
		t.Errorf("branches = %v", got)
	}
}
