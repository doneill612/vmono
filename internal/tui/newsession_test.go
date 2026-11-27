package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

func TestSessionPromptNamesOneBranchFromPrefixAndName(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"), env.NewSource(t, "beta"))
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.has("session:", "prefix:", "none", "build on:", "each member's base branch",
		"alpha  base (origin/HEAD)", "beta   base (origin/HEAD)", "Names this piece of work")
	d.lacks("branch:", "work on")
	d.typeText("PLAT_412")
	d.key("tab")
	d.typeText("feat")
	d.has("feat/PLAT_412")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	for _, got := range sessionBranches(t, env, "PLAT_412", "alpha", "beta") {
		if got != "feat/PLAT_412" {
			t.Errorf("branch = %q", got)
		}
	}
}

func TestSessionPromptTypesHyphensForSpaces(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("token")
	d.key("space")
	d.typeText("refresh")
	d.key("tab")
	d.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("my feat"), Paste: true})
	d.has("session:  token-refresh", "my-feat/token-refresh")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := sessionBranches(t, env, "token-refresh", "alpha"); got[0] != "my-feat/token-refresh" {
		t.Errorf("branches = %v", got)
	}
}

// pushBranch puts a commit of its own on the remote of src as branch, and
// returns it.
func pushBranch(t *testing.T, src, branch string) string {
	t.Helper()
	tip := testutil.Git(t, src, "commit-tree", "-m", branch, "-p", "main", "main^{tree}")
	testutil.Git(t, src, "push", "--quiet", "origin", tip+":refs/heads/"+branch)
	return tip
}

// head is the commit a member of a session of pay is at.
func head(t *testing.T, env *testutil.Env, session, member string) string {
	t.Helper()
	return testutil.Git(t, filepath.Join(env.PackageRoot, "pay", session, member), "rev-parse", "HEAD")
}

// pickBranch opens the picker of the focused field, waits for the branch
// typed to be listed, and picks it.
func pickBranch(d *driver, branch string) {
	d.t.Helper()
	d.key("space")
	d.eventually(func() bool { return !containsView(d, "fetching") && containsView(d, branch) }, "the branch "+branch)
	d.typeText(branch)
	d.key("enter")
}

func TestSessionPromptBuildsOnAPickedBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	tip := pushBranch(t, a, "parent")
	main := testutil.Git(t, b, "rev-parse", "main")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("child")
	d.key("tab", "tab", "space") // the build on field opens the picker
	d.has("build on a branch", "start from each member's base")
	d.eventually(func() bool { return containsView(d, "parent") }, "the fetched branch")
	d.typeText("parent")
	d.key("enter")
	d.has("build on: origin/parent  in alpha", "alpha  origin/parent", "base (origin/HEAD), lacks that branch")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := head(t, env, "child", "alpha"); got != tip {
		t.Errorf("alpha starts at %s, want %s", got, tip)
	}
	if got := head(t, env, "child", "beta"); got != main {
		t.Errorf("beta starts at %s, want its base %s", got, main)
	}
}

func TestSessionPromptBuildsEachMemberOnItsOwnChoice(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	pushBranch(t, a, "parent")
	pushBranch(t, b, "parent")
	second := pushBranch(t, b, "second")
	main := testutil.Git(t, a, "rev-parse", "main")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("child")
	d.key("tab", "tab")
	pickBranch(d, "parent")

	d.key("tab", "space") // alpha takes its base in place of the pick
	d.has("build alpha on", "same as build on above", "its base: origin/HEAD")
	d.key("down", "enter")
	d.has("alpha  base (origin/HEAD)")

	d.key("tab")
	pickBranch(d, "second")
	d.has("beta   origin/second")
	d.key("space", "enter") // and back to following build on
	d.has("beta   origin/parent")
	pickBranch(d, "second")

	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := head(t, env, "child", "alpha"); got != main {
		t.Errorf("alpha starts at %s, want its base %s", got, main)
	}
	if got := head(t, env, "child", "beta"); got != second {
		t.Errorf("beta starts at %s, want %s", got, second)
	}
}

// divergeBranch leaves branch of src, which must be checked out, one commit
// ahead of its remote copy and one behind it.
func divergeBranch(t *testing.T, src, branch string) (local, remote string) {
	t.Helper()
	remote = pushBranch(t, src, branch)
	testutil.Git(t, src, "commit", "--quiet", "--allow-empty", "-m", "unpushed")
	return testutil.Git(t, src, "rev-parse", "HEAD"), remote
}

// TestSessionPromptTellsALocalBranchFromTheRemotes covers the two rows a
// branch has once the local one differs: each is exactly what it says.
func TestSessionPromptTellsALocalBranchFromTheRemotes(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	local, remote := divergeBranch(t, a, "main")

	for session, tc := range map[string]struct {
		moves int
		shown string
		want  string
	}{
		"onlocal":  {0, "build on: main (local)  in alpha", local},
		"onremote": {1, "build on: origin/main  in alpha", remote},
	} {
		d := newDriver(t, env, "")
		d.key("right", "n")
		d.typeText(session)
		d.key("tab", "tab", "space")
		d.eventually(func() bool { return !containsView(d, "fetching") }, "the fetch")
		d.typeText("main")
		d.has("1 ahead, 1 behind origin/main", "origin/main")
		for range tc.moves {
			d.key("down")
		}
		d.key("enter")
		d.has(tc.shown)
		d.key("enter")
		d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
		if got := head(t, env, session, "alpha"); got != tc.want {
			t.Errorf("%s starts at %s, want %s", session, got, tc.want)
		}
	}
}

func TestSessionPromptChecksAReferenceOutAtALocalBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, lib := env.NewSource(t, "alpha"), env.NewSource(t, "lib")
	seedWithRefs(t, env, "pay", []string{a}, []string{lib})
	local, _ := divergeBranch(t, lib, "main")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("work")
	d.key("tab", "tab", "tab", "tab") // prefix, build on, alpha, lib
	pickBranch(d, "main")
	d.has("read-only lib    main (local)")
	d.key("space", "enter") // none empties it again
	d.has("origin/HEAD")
	d.lacks("(local)")
	pickBranch(d, "main")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	dir := filepath.Join(env.PackageRoot, "pay", "work", "_ref", "lib")
	if got := testutil.Git(t, dir, "rev-parse", "HEAD"); got != local {
		t.Errorf("lib is at %s, want the local branch %s", got, local)
	}
}

func TestSessionPromptRefusesBuildingOnItsOwnBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	pushBranch(t, a, "parent")
	d := newDriver(t, env, "")
	const clash = "build on cannot be parent, the session's own branch"

	// The pick comes first, and the name is then typed to match it.
	d.key("right", "n", "tab", "tab")
	pickBranch(d, "parent")
	d.lacks(clash)
	d.key("shift+tab", "shift+tab")
	d.typeText("parent")
	d.has(clash)
	d.key("enter")
	d.has("new session", clash)
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "parent")); !os.IsNotExist(err) {
		t.Error("nothing should be made")
	}

	// With the name there first, the picker does not offer it.
	d.key("tab", "tab", "space")
	d.eventually(func() bool { return !containsView(d, "fetching") }, "the fetch")
	d.typeText("parent")
	d.has("no match")
}

func TestSessionPromptAsksBeforeWorkingOnARemoteBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	base := pushBranch(t, a, "parent")
	pushBranch(t, b, "parent")
	tip := pushBranch(t, a, "feat/x")
	testutil.Git(t, a, "update-ref", "-d", "refs/remotes/origin/feat/x") // only a fetch reveals it
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("x")
	d.key("tab")
	d.typeText("feat")
	d.key("tab")
	pickBranch(d, "parent")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Work on that branch?") }, "the question")
	d.has(`Branch "feat/x" is already on the remote of alpha.`,
		"alpha continues it there, so it is not built on origin/parent.",
		"beta does not have it, and starts a new branch of that name on origin/parent.")

	d.key("n") // back to the form, as it was, with nothing made
	d.has("new session", "feat/x", "build on: origin/parent", "Names this piece of work")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "pay", "x")); !os.IsNotExist(err) {
		t.Error("nothing should be made")
	}

	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Work on that branch?") }, "the question again")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	if got := head(t, env, "x", "alpha"); got != tip {
		t.Errorf("alpha is at %s, want the remote branch %s", got, tip)
	}
	if got := head(t, env, "x", "beta"); got == base || got == testutil.Git(t, b, "rev-parse", "main") {
		t.Errorf("beta should start on its parent branch, not %s", got)
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
	d.has("build on:", "alpha", "read-only lib        origin/HEAD", "read-only contracts  origin/HEAD")
	d.typeText("work")
	d.key("tab", "tab", "tab", "tab") // prefix, build on, alpha, lib
	d.has("press space to pick")
	d.key(" ")
	d.eventually(func() bool { return containsView(d, "feat/api") }, "the fetched branch")
	d.has("branch for lib", "none: the package's ref")
	d.lacks("feat/other")
	d.typeText("feat")
	d.key("enter")
	d.has("new session", "read-only lib        origin/feat/api")
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

func TestSessionPromptExplainsTheFocusedField(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	seedWithRefs(t, env, "pay", []string{env.NewSource(t, "alpha")}, []string{env.NewSource(t, "lib")})
	d := newDriver(t, env, "")

	d.key("right", "n")
	// In the order tab visits them: name, prefix, build on, the active member,
	// the read-only member.
	for i, want := range []string{
		"Names this piece of work", "(--prefix)", "Optional (--from)",
		"(--from MEMBER=REF), if", "checked out at (--from MEMBER=REF)",
	} {
		if i > 0 {
			d.key("tab")
		}
		d.has(want)
		for _, line := range d.app.top().(*sessionPrompt).fieldHelp() {
			if len(line) > 68 {
				t.Errorf("help is cut off at 80 columns: %q", line)
			}
		}
	}
}
