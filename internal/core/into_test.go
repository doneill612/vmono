package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

// featureBranch creates branch in src from main with one extra commit, and
// returns the new tip. The source's checkout stays on main.
func featureBranch(t *testing.T, src, branch string) string {
	t.Helper()
	testutil.Git(t, src, "checkout", "--quiet", "-b", branch)
	testutil.WriteFile(t, filepath.Join(src, branch+".txt"), branch+"\n")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "--quiet", "-m", "work on "+branch)
	tip, err := gitx.ResolveRef(src, branch)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, src, "checkout", "--quiet", "main")
	return tip
}

func TestCreateSessionRecordsInto(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("landing", []string{a}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature/x"})
	if err != nil {
		t.Fatal(err)
	}
	if s.S.Into["alpha"] != "feature/x" {
		t.Errorf("into = %v", s.S.Into)
	}
	loaded, err := c.LoadSession(p, "work")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.S.Into["alpha"] != "feature/x" {
		t.Errorf("into not persisted: %v", loaded.S.Into)
	}
	main, _ := gitx.ResolveRef(a, "origin/main")
	if tip, _ := gitx.ResolveRef(filepath.Join(s.Dir, "alpha"), "HEAD"); tip != main {
		t.Errorf("a landing branch that does not exist yet must leave the session cut from base: HEAD %s, origin/main %s", tip, main)
	}
	if gitx.BranchExists(a, "feature/x") {
		t.Error("the landing branch is created at finish, not at session create")
	}
}

func TestCreateSessionCutsFromExistingInto(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	tip := featureBranch(t, a, "feature")
	p := mustPackage(t, c, request("stack", []string{a}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(s.Dir, "alpha")
	if got, _ := gitx.ResolveRef(wt, "HEAD"); got != tip {
		t.Errorf("session branch starts at %s, want the tip of feature %s", got, tip)
	}
	if _, err := os.Stat(filepath.Join(wt, "feature.txt")); err != nil {
		t.Errorf("the session should contain the landing branch's files: %v", err)
	}

	plain, err := c.CreateSession(p, "plain", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	main, _ := gitx.ResolveRef(a, "origin/main")
	if got, _ := gitx.ResolveRef(filepath.Join(plain.Dir, "alpha"), "HEAD"); got != main {
		t.Errorf("a session without --into must still be cut from base: %s, want %s", got, main)
	}
}

func TestCreateSessionCutsFromRemoteOnlyInto(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	tip := featureBranch(t, a, "feature")
	testutil.Git(t, a, "push", "--quiet", "origin", "feature")
	testutil.Git(t, a, "branch", "-D", "feature")
	p := mustPackage(t, c, request("remote-only", []string{a}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := gitx.ResolveRef(filepath.Join(s.Dir, "alpha"), "HEAD"); got != tip {
		t.Errorf("session branch starts at %s, want origin/feature %s", got, tip)
	}
}

func TestCreateSessionRejectsBadInto(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "branch", "feat")
	p := mustPackage(t, c, request("badinto", []string{a}, nil))

	for name, into := range map[string]string{
		"not a branch name":  "has space",
		"leading dash":       "-x",
		"git cannot hold":    "feat/x",
		"the session itself": "vmono/badinto/work",
	} {
		if _, err := c.CreateSession(p, "work", SessionOptions{Into: into}); err == nil {
			t.Errorf("%s: into %q was accepted", name, into)
		}
		if _, err := os.Stat(p.SessionDir("work")); !os.IsNotExist(err) {
			t.Errorf("%s: a refused session must leave no directory", name)
		}
	}
}

// commitIn writes a file in dir and commits it.
func commitIn(t *testing.T, dir, name string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, name), name+"\n")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "add "+name)
}

func TestCommitsOnTheLandingBranchAreSafeToDelete(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("landed", []string{a}, nil))
	wt := filepath.Join(s.Dir, "alpha")

	commitIn(t, wt, "work.txt")
	rows := c.SafetyCheck(s)
	if len(rows) != 1 || rows[0].Unpushed != 1 {
		t.Fatalf("a commit on no remote and no landing branch must count: %+v", rows)
	}

	// Land it by hand on the source's local main, which is not pushed anywhere.
	testutil.Git(t, a, "merge", "--ff-only", "--quiet", s.S.Branch)
	rows = c.SafetyCheck(s)
	if bad := unsafeOf(rows); len(bad) != 0 {
		t.Fatalf("work that landed on main must not block removal: %+v", bad)
	}

	res, err := c.DeleteSession(s, DeleteOptions{DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if gitx.BranchExists(a, s.S.Branch) {
		t.Errorf("a branch merged into the landing branch should be deleted: %+v", res)
	}
	if !gitx.BranchExists(a, "main") {
		t.Fatal("the landing branch itself must survive")
	}
	if _, err := c.Load(p.Name); err != nil {
		t.Errorf("deleting a session must keep its package: %v", err)
	}
}

func TestExplicitIntoDecidesWhatCountsAsLanded(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	featureBranch(t, a, "feature")
	p := mustPackage(t, c, request("explicit", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	// main is not where this session lands, so landing there proves nothing
	// about feature; the session still has work only its own branch holds.
	if rows := c.SafetyCheck(s); rows[0].Unpushed != 1 {
		t.Fatalf("before landing: %+v", rows)
	}
	// Move feature itself to the session's tip, without touching main.
	tip, _ := gitx.ResolveRef(a, s.S.Branch)
	if err := gitx.UpdateRefCAS(a, "refs/heads/feature", tip, "", "test"); err == nil {
		t.Fatal("feature exists already; creating it must fail")
	}
	old, _ := gitx.ResolveRef(a, "feature")
	if err := gitx.UpdateRefCAS(a, "refs/heads/feature", tip, old, "test: land"); err != nil {
		t.Fatal(err)
	}
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 0 {
		t.Fatalf("work on the explicit landing branch must be safe: %+v", bad)
	}
}

func TestSessionRecordsEachMembersCheckedOutBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	g := env.NewSource(t, "gamma")
	// beta is checked out on a branch with work of its own; gamma is detached.
	devTip := featureBranch(t, b, "develop")
	testutil.Git(t, b, "checkout", "--quiet", "develop")
	testutil.Git(t, g, "checkout", "--quiet", "--detach")
	p := mustPackage(t, c, request("capture", []string{a, b, g}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s.S.Into["alpha"] != "main" || s.S.Into["beta"] != "develop" {
		t.Errorf("into = %v, want each member's checked-out branch", s.S.Into)
	}
	if _, ok := s.S.Into["gamma"]; ok {
		t.Errorf("a detached source has no checked-out branch to record: %v", s.S.Into)
	}
	// beta lands on a branch other than its base's, so the session builds on it;
	// alpha lands on its base's branch, so it starts from origin/main as usual.
	if got := sha(t, filepath.Join(s.Dir, "beta"), "HEAD"); got != devTip {
		t.Errorf("beta session starts at %s, want develop's tip %s", got, devTip)
	}
	if got, want := sha(t, filepath.Join(s.Dir, "alpha"), "HEAD"), sha(t, a, "origin/main"); got != want {
		t.Errorf("alpha session starts at %s, want origin/main %s", got, want)
	}
	if st := c.Status(s); st.Members[1].Into != "develop" || st.Members[0].Into != "main" || st.Members[2].Into != "main" {
		t.Errorf("status landing branches = %q %q %q", st.Members[0].Into, st.Members[1].Into, st.Members[2].Into)
	}
}

func TestLandingOnTheBaseBranchStillStartsFromBase(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	// A local commit on main that origin lacks must not become the start point.
	commitIn(t, a, "local-only.txt")
	p := mustPackage(t, c, request("lag", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sha(t, filepath.Join(s.Dir, "alpha"), "HEAD"), sha(t, a, "origin/main"); got != want {
		t.Errorf("session starts at %s, want origin/main %s", got, want)
	}
}

func TestIntoByMemberOverridesTheCheckedOutBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	p := mustPackage(t, c, request("override", []string{a, b}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{Into: "release", IntoByMember: map[string]string{"beta": "hotfix"}})
	if err != nil {
		t.Fatal(err)
	}
	if s.S.Into["alpha"] != "release" || s.S.Into["beta"] != "hotfix" {
		t.Errorf("into = %v", s.S.Into)
	}
	if _, err := c.CreateSession(p, "other", SessionOptions{IntoByMember: map[string]string{"ghost": "main"}}); err == nil {
		t.Error("a member that is not in the package must be rejected")
	}
	if _, err := os.Stat(p.SessionDir("other")); !os.IsNotExist(err) {
		t.Error("a refused session must leave no directory")
	}
}

// TestReferenceOnALocalBranchIsSafeToDelete covers a source with no
// origin/HEAD, whose reference members sit on the branch it has checked out.
// Its commits stay in the source repo, so removing the worktree loses nothing,
// but commits made on the detached HEAD itself would be lost.
func TestReferenceOnALocalBranchIsSafeToDelete(t *testing.T) {
	c, env := newCore(t)
	a, ref := env.NewSource(t, "alpha"), env.NewSource(t, "contracts")
	testutil.Git(t, ref, "remote", "set-head", "origin", "--delete")
	testutil.Git(t, ref, "checkout", "--quiet", "-b", "feature")
	commitIn(t, ref, "local.txt")
	_, s := newWork(t, c, request("refs", []string{a}, []string{ref}))
	wt := filepath.Join(s.Dir, "_ref", "contracts")
	if sha(t, wt, "HEAD") != sha(t, ref, "feature") {
		// git 2.48 and newer set origin/HEAD again on fetch.
		testutil.Git(t, wt, "checkout", "--quiet", "--detach", "feature")
	}
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 0 {
		t.Errorf("unsafe = %+v", bad)
	}

	commitIn(t, wt, "stray.txt")
	bad := unsafeOf(c.SafetyCheck(s))
	if len(bad) != 1 || bad[0].Name != "contracts" || bad[0].Unpushed != 1 {
		t.Errorf("a commit only the worktree holds must be unsafe: %+v", bad)
	}
	testutil.Git(t, wt, "reset", "--quiet", "--hard", "HEAD~1")
	if _, err := c.Finish(s, FinishOptions{Close: true}); err != nil {
		t.Errorf("finish and close: %v", err)
	}
}
