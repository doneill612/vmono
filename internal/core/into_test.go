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
	if s.S.Into != "feature/x" {
		t.Errorf("into = %q", s.S.Into)
	}
	loaded, err := c.LoadSession(p, "work")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.S.Into != "feature/x" {
		t.Errorf("into not persisted: %q", loaded.S.Into)
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
