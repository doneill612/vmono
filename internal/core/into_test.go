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
