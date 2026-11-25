package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
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

// ownCommitsOf returns the member's count of commits its base lacks.
func ownCommitsOf(t *testing.T, c *Core, s *Session, name string) int {
	t.Helper()
	for _, m := range c.Status(s).Members {
		if m.Name == name {
			return m.OwnCommits
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
	if n := ownCommitsOf(t, c, s, "alpha"); n != 1 {
		t.Errorf("own commits = %d, want 1", n)
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

// TestUpdateLeavesTheSourcesBranchesOut covers a local main that has commits of
// its own: the session is built on origin/main, and nothing else.
func TestUpdateLeavesTheSourcesBranchesOut(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("div", []string{a}, nil))
	commitIn(t, a, "local.txt")
	pushFromElsewhere(t, a, "main", "new.txt", "new\n")

	if _, err := c.Update(s, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := sha(t, filepath.Join(s.Dir, "alpha"), "HEAD"); got != sha(t, a, "origin/main") {
		t.Errorf("session is at %s, want origin/main", got)
	}
}

func TestUpdateMergesIntoAPublishedBranch(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, wt, branch := srcs["alpha"], filepath.Join(s.Dir, "alpha"), s.S.Branch
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	pushed := sha(t, wt, "HEAD")

	// Someone else's commit on the branch is a fast-forward.
	pushFromElsewhere(t, a, branch, "theirs.txt", "theirs\n")
	res, err := c.Update(s, UpdateOptions{})
	if m := res.Members[0]; err != nil || m.Status != UpdateDone || !m.Merge || m.Behind != 1 || m.Onto != "origin/"+branch {
		t.Fatalf("update = %+v, %v", m, err)
	}
	if sha(t, wt, "HEAD") != sha(t, a, "refs/remotes/origin/"+branch) {
		t.Error("a branch with nothing new of its own should fast-forward")
	}

	// New commits on both sides and on the base are merged, none rewritten.
	commitIn(t, wt, "mine.txt")
	mine := sha(t, wt, "HEAD")
	pushFromElsewhere(t, a, branch, "more.txt", "more\n")
	pushFromElsewhere(t, a, "main", "base.txt", "base\n")
	res, err = c.Update(s, UpdateOptions{DryRun: true})
	if m := res.Members[0]; err != nil || m.Status != UpdateReady || m.Onto != "origin/"+branch+" and origin/main" || sha(t, wt, "HEAD") != mine {
		t.Fatalf("dry run = %+v, %v", m, err)
	}
	if _, err := c.Update(s, UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{pushed, mine, "origin/main", "refs/remotes/origin/" + branch} {
		if !gitx.IsMerged(wt, ref, "HEAD") {
			t.Errorf("%s should be in the branch", ref)
		}
	}
	res2, err := c.Push(s, PushOptions{})
	if m := pushRow(t, res2, "alpha"); err != nil || m.Status != PushPushed {
		t.Errorf("the push should be a fast-forward: %+v, %v", m, err)
	}
	if res, err := c.Update(s, UpdateOptions{}); err != nil || res.Members[0].Status != UpdateCurrent {
		t.Errorf("again = %+v, %v", res, err)
	}
}

func TestUpdateMergeConflictLeavesTheMemberAsItWas(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, wt, branch := srcs["alpha"], filepath.Join(s.Dir, "alpha"), s.S.Branch
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "session\n")
	testutil.Git(t, wt, "commit", "--quiet", "-am", "session edit")
	pushFromElsewhere(t, a, branch, "theirs.txt", "theirs\n")
	pushFromElsewhere(t, a, "main", "README.md", "upstream\n")
	tip := sha(t, wt, "HEAD")

	for range 2 { // what the first leaves in the reflogs must not stop the second
		res, err := c.Update(s, UpdateOptions{})
		if m := res.Members[0]; !errors.Is(err, ErrRefused) || m.Status != UpdateConflict || len(m.Conflicts) != 1 || !strings.Contains(m.Reason, "git merge origin/main") {
			t.Fatalf("update = %+v, %v", m, err)
		}
		if sha(t, wt, "HEAD") != tip || testutil.Git(t, wt, "status", "--porcelain") != "" {
			t.Error("both merges must be undone")
		}
	}
}

func TestUpdateLeavesMergedWorkAlone(t *testing.T) {
	for _, how := range []string{MergedCommit, MergedSquash, MergedRebase} {
		c, env := newCore(t)
		s, srcs := workSession(t, c, env, "alpha")
		a, wt := srcs["alpha"], filepath.Join(s.Dir, "alpha")
		commitIn(t, wt, "more.txt")
		if _, err := c.Push(s, PushOptions{}); err != nil {
			t.Fatal(err)
		}
		mergeOnRemote(t, a, s.S.Branch, how, true)
		tip := sha(t, wt, "HEAD")

		res, err := c.Update(s, UpdateOptions{})
		if m := res.Members[0]; err != nil || m.Status != UpdateMerged || !strings.Contains(m.Reason, "origin/main") || sha(t, wt, "HEAD") != tip {
			t.Errorf("%s: %+v, %v", how, m, err)
		}
		commitIn(t, wt, "late.txt")
		res, err = c.Update(s, UpdateOptions{})
		if m := res.Members[0]; !errors.Is(err, ErrRefused) || m.Status != UpdateSkipped || !strings.Contains(m.Reason, "was deleted") {
			t.Errorf("%s, late commit: %+v, %v", how, m, err)
		}
	}
}

func TestUpdateSkipsRewrittenHistory(t *testing.T) {
	setup := func(t *testing.T) (c *Core, s *Session, a, wt, clone string) {
		c, env := newCore(t)
		s, srcs := workSession(t, c, env, "alpha")
		a, wt = srcs["alpha"], filepath.Join(s.Dir, "alpha")
		if _, err := c.Push(s, PushOptions{}); err != nil {
			t.Fatal(err)
		}
		clone = filepath.Join(t.TempDir(), "clone")
		testutil.Git(t, a, "clone", "--quiet", "--branch", s.S.Branch, remoteOf(env, "alpha"), clone)
		return c, s, a, wt, clone
	}
	check := func(t *testing.T, c *Core, s *Session, wt, want string) {
		t.Helper()
		tip := sha(t, wt, "HEAD")
		res, err := c.Update(s, UpdateOptions{})
		if m := res.Members[0]; !errors.Is(err, ErrRefused) || m.Status != UpdateSkipped || !strings.Contains(m.Reason, want) || sha(t, wt, "HEAD") != tip {
			t.Errorf("update = %+v, %v", m, err)
		}
	}
	t.Run("remote", func(t *testing.T) {
		c, s, _, wt, clone := setup(t)
		testutil.Git(t, clone, "commit", "--quiet", "--amend", "-m", "reworded")
		testutil.Git(t, clone, "push", "--quiet", "--force", "origin", s.S.Branch)
		check(t, c, s, wt, "was rewritten since")
	})
	t.Run("remote, fetched before", func(t *testing.T) {
		c, s, a, wt, clone := setup(t)
		testutil.Git(t, clone, "commit", "--quiet", "--amend", "-m", "reworded")
		testutil.Git(t, clone, "push", "--quiet", "--force", "origin", s.S.Branch)
		testutil.Git(t, a, "fetch", "--quiet")
		check(t, c, s, wt, "was rewritten since")
	})
	t.Run("local", func(t *testing.T) {
		c, s, a, wt, _ := setup(t)
		testutil.Git(t, wt, "commit", "--quiet", "--amend", "-m", "reworded")
		pushFromElsewhere(t, a, "main", "new.txt", "new\n")
		check(t, c, s, wt, "rewritten after it was pushed")
	})
}

func TestUpdateTracksABranchPushedByHand(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, branch := srcs["alpha"], s.S.Branch
	testutil.Git(t, a, "push", "--quiet", "origin", branch)
	pushFromElsewhere(t, a, branch, "theirs.txt", "theirs\n")
	if res, err := c.Update(s, UpdateOptions{}); err != nil || res.Members[0].Status != UpdateDone {
		t.Fatalf("update = %+v, %v", res, err)
	}
	if tr, ok := gitx.UpstreamOf(a, branch); !ok || tr.Branch != branch {
		t.Errorf("upstream = %+v, %v", tr, ok)
	}
}

func TestUpdateSkipsAnUnrelatedBranchOfTheSameName(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	pushFromElsewhere(t, srcs["alpha"], s.S.Branch, "theirs.txt", "theirs\n")
	res, err := c.Update(s, UpdateOptions{})
	if m := res.Members[0]; !errors.Is(err, ErrRefused) || m.Status != UpdateSkipped || !strings.Contains(m.Reason, "shares no work") {
		t.Errorf("update = %+v, %v", m, err)
	}
}

// stacked makes a session on the branch "parent" of alpha, pushed with two
// commits, and one built on it with a commit of its own.
func stacked(t *testing.T, c *Core, env *testutil.Env) (parent, child *Session, a string) {
	t.Helper()
	a = env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("stack", []string{a}, nil))
	parent, err := c.CreateSession(p, "parent", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(parent.Dir, "alpha"), "p1.txt")
	commitIn(t, filepath.Join(parent.Dir, "alpha"), "p2.txt")
	if _, err := c.Push(parent, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if child, err = c.CreateSession(p, "child", SessionOptions{From: "parent"}); err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(child.Dir, "alpha"), "c.txt")
	return parent, child, a
}

func TestUpdateFollowsTheBranchItIsBuiltOn(t *testing.T) {
	c, env := newCore(t)
	parent, child, _ := stacked(t, c, env)
	pwt, cwt := filepath.Join(parent.Dir, "alpha"), filepath.Join(child.Dir, "alpha")

	commitIn(t, pwt, "p3.txt")
	res, err := c.Update(child, UpdateOptions{})
	if m := res.Members[0]; err != nil || m.Status != UpdateDone || m.Onto != "parent" || m.Commits != 1 {
		t.Fatalf("update = %+v, %v", m, err)
	}
	if tip := child.S.Members["alpha"].BaseTip; tip != sha(t, pwt, "HEAD") || !gitx.IsMerged(cwt, tip, "HEAD") {
		t.Errorf("base tip = %s", tip)
	}

	// The parent being rebased moves the child's commit alone.
	testutil.Git(t, pwt, "commit", "--quiet", "--amend", "-m", "reworded")
	if res, err = c.Update(child, UpdateOptions{}); err != nil || res.Members[0].Commits != 1 {
		t.Fatalf("after a rewrite = %+v, %v", res, err)
	}
	if n := ownCommitsOf(t, c, child, "alpha"); n != 1 {
		t.Errorf("own commits = %d, want 1", n)
	}
}

// TestUpdateMovesToTheBaseOnceWhatItIsBuiltOnMerged covers a squash merge of the
// parent, whose own commits must not come along.
func TestUpdateMovesToTheBaseOnceWhatItIsBuiltOnMerged(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent gone=%v", gone), func(t *testing.T) {
			c, env := newCore(t)
			parent, child, a := stacked(t, c, env)
			cwt := filepath.Join(child.Dir, "alpha")
			mergeOnRemote(t, a, "parent", MergedSquash, true)
			if gone {
				testutil.Git(t, a, "fetch", "--quiet", "--prune")
				if _, err := c.DeleteSession(parent, DeleteOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
				testutil.Git(t, a, "branch", "-D", "parent")
			}

			res, err := c.Update(child, UpdateOptions{})
			m := res.Members[0]
			if err != nil || m.Status != UpdateDone || m.Onto != "origin/main" || m.Commits != 1 || len(m.Notes) != 1 {
				t.Fatalf("update = %+v, %v", m, err)
			}
			if got := testutil.Git(t, cwt, "rev-list", "--count", "origin/main..HEAD"); got != "1" {
				t.Errorf("%s commits on top of main, want only the child's", got)
			}
			loaded, err := c.LoadSession(child.Pkg, "child")
			if err != nil || loaded.S.Members["alpha"].From != "" || loaded.S.Members["alpha"].BaseTip != "" {
				t.Errorf("members = %+v, %v", loaded.S.Members, err)
			}
			if res, err := c.Update(child, UpdateOptions{}); err != nil || res.Members[0].Status != UpdateCurrent {
				t.Errorf("again = %+v, %v", res, err)
			}
		})
	}
}

func TestUpdateKeepsToABranchThatIsGoneWithoutHavingMerged(t *testing.T) {
	c, env := newCore(t)
	parent, child, a := stacked(t, c, env)
	if _, err := c.DeleteSession(parent, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, a, "branch", "-D", "parent")
	testutil.Git(t, a, "push", "--quiet", "origin", "--delete", "parent")
	tip := sha(t, filepath.Join(child.Dir, "alpha"), "HEAD")

	res, err := c.Update(child, UpdateOptions{})
	if m := res.Members[0]; !errors.Is(err, ErrRefused) || m.Status != UpdateSkipped || !strings.Contains(m.Reason, "does not look merged") {
		t.Errorf("update = %+v, %v", m, err)
	}
	if sha(t, filepath.Join(child.Dir, "alpha"), "HEAD") != tip {
		t.Error("the parent's commits must not be dropped")
	}
}
