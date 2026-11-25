package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// sessionBranches returns the branch each member of a session of pay is on.
func sessionBranches(t *testing.T, env *testutil.Env, session string, members ...string) []string {
	t.Helper()
	var out []string
	for _, m := range members {
		out = append(out, testutil.Git(t, filepath.Join(env.PackageRoot, "pay", session, m), "branch", "--show-current"))
	}
	return out
}

func TestSessionPromptPrefixAndMemberBranches(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"), env.NewSource(t, "beta"))
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.has("prefix:", "none", "branch alpha", "branch beta", "each member's base")
	d.typeText("retry")
	d.key("tab")
	d.typeText("feat")
	d.has("branch:  feat/retry", "branch alpha  feat/retry")
	d.key("tab", "tab", "tab", "tab", "tab") // branch, track, from, alpha, beta
	d.typeText("other")
	d.key("tab", "shift+tab") // away and back: the text stays
	d.has("other")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := sessionBranches(t, env, "retry", "alpha", "beta"); got[0] != "feat/retry" || got[1] != "other" {
		t.Errorf("branches = %v", got)
	}
}

func TestSessionPromptBuildsOnAPickedBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	tip := testutil.Git(t, a, "commit-tree", "-m", "parent", "-p", "main", "main^{tree}")
	testutil.Git(t, a, "push", "--quiet", "origin", tip+":refs/heads/parent")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("child")
	d.key("tab", "tab", "tab", "tab", "enter") // the from field opens the picker
	d.has("build on a remote branch", "start from each member's base")
	d.eventually(func() bool { return containsView(d, "parent") }, "the fetched branch")
	d.typeText("parent")
	d.key("enter")
	d.has("new session", "from:    parent  (alpha)", "branch:  child")
	d.key("tab", "enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := sessionBranches(t, env, "child", "alpha"); got[0] != "child" {
		t.Errorf("branches = %v", got)
	}
	if got := testutil.Git(t, filepath.Join(env.PackageRoot, "pay", "child", "alpha"), "rev-parse", "HEAD"); got != tip {
		t.Errorf("starts at %s, want %s", got, tip)
	}
}

func TestSessionPromptPicksWhatAReferenceIsCheckedOutAt(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, lib, api := env.NewSource(t, "alpha"), env.NewSource(t, "lib"), env.NewSource(t, "contracts")
	seedWithRefs(t, env, "pay", []string{a}, []string{lib, api})
	tip := testutil.Git(t, lib, "commit-tree", "-m", "wip", "-p", "main", "main^{tree}")
	testutil.Git(t, lib, "push", "--quiet", "origin", tip+":refs/heads/feat/api")
	testutil.Git(t, lib, "update-ref", "-d", "refs/remotes/origin/feat/api") // only a fetch reveals it
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/other")
	old := testutil.Git(t, api, "rev-parse", "main")
	testutil.Git(t, api, "tag", "v1")
	testutil.Git(t, api, "commit", "--quiet", "--allow-empty", "-m", "later")
	testutil.Git(t, api, "push", "--quiet")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.has("branch alpha", "ref    lib        origin/HEAD", "ref    contracts  origin/HEAD")
	d.typeText("work")
	d.key("tab", "tab", "tab", "tab", "tab", "tab") // prefix, branch, track, from, alpha, lib
	d.has("press space to pick")
	d.key(" ")
	d.eventually(func() bool { return containsView(d, "feat/api") }, "the fetched branch")
	d.has("remote branch for lib", "none: the package's ref")
	d.lacks("feat/other")
	d.typeText("feat")
	d.key("enter")
	d.has("new session", "ref    lib        feat/api")
	d.key("tab")
	d.typeText("v1")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	dir := filepath.Join(env.PackageRoot, "pay", "work")
	for member, want := range map[string]string{"lib": tip, "contracts": old} {
		if got := testutil.Git(t, filepath.Join(dir, "_ref", member), "rev-parse", "HEAD"); got != want {
			t.Errorf("%s is at %s, want %s", member, got, want)
		}
	}
	if got := sessionBranches(t, env, "work", "alpha"); got[0] != "work" {
		t.Errorf("branches = %v", got)
	}
}
