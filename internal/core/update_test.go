package core

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// pushFromElsewhere commits content to file on branch of src's remote from a
// separate clone, creating the branch if needed, so src has not seen it until
// it fetches.
func pushFromElsewhere(t *testing.T, src, branch, file, content string) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, src, "clone", "--quiet", testutil.Git(t, src, "remote", "get-url", "origin"), clone)
	if gitx.RefExists(clone, "refs/remotes/origin/"+branch) {
		testutil.Git(t, clone, "checkout", "--quiet", branch)
	} else {
		testutil.Git(t, clone, "checkout", "--quiet", "-b", branch)
	}
	testutil.WriteFile(t, filepath.Join(clone, file), content)
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", "elsewhere: "+file)
	testutil.Git(t, clone, "push", "--quiet", "origin", branch)
}

// toLand returns the member's count of commits left to land.
func toLand(t *testing.T, c *Core, s *Session, name string) int {
	t.Helper()
	for _, m := range c.Status(s).Members {
		if m.Name == name {
			return m.ToLand
		}
	}
	t.Fatalf("no member %q", name)
	return 0
}

func TestUpdateRebasesOntoTheRemote(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("up", []string{a, b}, nil))
	wt := filepath.Join(s.Dir, "alpha")
	commitIn(t, wt, "work.txt")
	pushFromElsewhere(t, a, "main", "new.txt", "new\n")
	main, tip := sha(t, a, "main"), sha(t, wt, "HEAD")

	res, err := c.Update(s, UpdateOptions{DryRun: true})
	if err != nil || res.Members[0].Status != UpdateReady || sha(t, wt, "HEAD") != tip {
		t.Fatalf("dry run = %+v, %v", res, err)
	}
	if res, err = c.Update(s, UpdateOptions{Members: []string{"beta"}}); err != nil || len(res.Members) != 1 || sha(t, wt, "HEAD") != tip {
		t.Fatalf("--member beta = %+v, %v", res, err)
	}
	res, err = c.Update(s, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.Members[0]; m.Status != UpdateDone || m.Onto != "origin/main" || m.Behind != 1 || m.Commits != 1 {
		t.Errorf("alpha = %+v", m)
	}
	if m := res.Members[1]; m.Status != UpdateCurrent {
		t.Errorf("beta = %+v", m)
	}
	if !gitx.IsMerged(wt, "origin/main", "HEAD") || sha(t, a, "main") != main {
		t.Error("the session should contain origin/main, and the source's main must not move")
	}
	if n := toLand(t, c, s, "alpha"); n != 1 {
		t.Errorf("to land = %d, want 1", n)
	}
	if _, err := c.Update(s, UpdateOptions{Members: []string{"nope"}}); err == nil {
		t.Error("an unknown member must be rejected")
	}
}

func TestUpdateSkipsUncommittedWorkOnlyWhenBehind(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("dirty", []string{a, b}, nil))
	for _, name := range []string{"alpha", "beta"} {
		testutil.WriteFile(t, filepath.Join(s.Dir, name, "README.md"), "edited\n")
	}
	pushFromElsewhere(t, a, "main", "new.txt", "new\n")

	res, err := c.Update(s, UpdateOptions{})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if res.Members[0].Status != UpdateSkipped || res.Members[1].Status != UpdateCurrent {
		t.Errorf("members = %+v", res.Members)
	}
}

func TestUpdateAbortsAConflictAndUpdatesTheRest(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("conf", []string{a, b}, nil))
	wt := filepath.Join(s.Dir, "alpha")
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "session\n")
	testutil.Git(t, wt, "commit", "--quiet", "-am", "session edit")
	tip := sha(t, wt, "HEAD")
	pushFromElsewhere(t, a, "main", "README.md", "upstream\n")
	pushFromElsewhere(t, b, "main", "new.txt", "new\n")

	res, err := c.Update(s, UpdateOptions{})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if m := res.Members[0]; m.Status != UpdateConflict || len(m.Conflicts) != 1 {
		t.Errorf("alpha = %+v", m)
	}
	if res.Members[1].Status != UpdateDone {
		t.Errorf("beta = %+v", res.Members[1])
	}
	if gitx.RebaseInProgress(wt) || sha(t, wt, "HEAD") != tip {
		t.Error("a conflicted member must be left as it was")
	}
}

func TestUpdateSkipsADivergedLandingBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("div", []string{a}, nil))
	commitIn(t, a, "local.txt")
	pushFromElsewhere(t, a, "main", "new.txt", "new\n")
	tip := sha(t, s.MemberDir(manifest.Member{Path: "alpha"}), "HEAD")

	res, err := c.Update(s, UpdateOptions{})
	if !errors.Is(err, ErrRefused) || res.Members[0].Status != UpdateSkipped {
		t.Errorf("update = %+v, %v", res, err)
	}
	if sha(t, filepath.Join(s.Dir, "alpha"), "HEAD") != tip {
		t.Error("the session must not move")
	}
}

func TestUpdateMergeConflictLeavesTheMemberAsItWas(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("mc", []string{a}, nil))
	wt := filepath.Join(s.Dir, "alpha")
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "session\n")
	testutil.Git(t, wt, "commit", "--quiet", "-am", "session edit")
	commitIn(t, a, "local.txt")
	pushFromElsewhere(t, a, "main", "README.md", "upstream\n")
	tip := sha(t, wt, "HEAD")

	res, err := c.Update(s, UpdateOptions{Merge: true})
	if !errors.Is(err, ErrRefused) || res.Members[0].Status != UpdateConflict || len(res.Members[0].Conflicts) != 1 {
		t.Fatalf("update = %+v, %v", res, err)
	}
	if sha(t, wt, "HEAD") != tip || testutil.Git(t, wt, "status", "--porcelain") != "" {
		t.Error("both merges must be undone")
	}
}
