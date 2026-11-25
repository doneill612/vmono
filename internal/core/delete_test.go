package core

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// fate returns what removing the named member would do with its branch.
func fate(t *testing.T, c *Core, s *Session, name string) MemberSafety {
	t.Helper()
	for _, m := range c.SafetyCheck(s) {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no member %q", name)
	return MemberSafety{}
}

func TestRemovingASessionDeletesBranchesWithNothingOnThem(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	testutil.Git(t, b, "branch", "work")
	p := mustPackage(t, c, request("empty", []string{a, b}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{ReuseBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	pushFromElsewhere(t, a, "main", "new.txt", "new\n")
	if _, err := c.Update(s, UpdateOptions{Members: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	if m := fate(t, c, s, "alpha"); m.Fate != BranchEmpty || m.Branch != "work" {
		t.Errorf("alpha = %+v", m)
	}
	if m := fate(t, c, s, "beta"); m.Fate != BranchMerged {
		t.Errorf("beta's branch was there before, so it goes only when asked: %+v", m)
	}
	res, err := c.DeleteSession(s, DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if gitx.BranchExists(a, "work") || !gitx.BranchExists(b, "work") {
		t.Errorf("result = %+v", res)
	}
	if len(res.BranchesDeleted) != 1 || res.BranchesDeleted[0] != (BranchOutcome{Session: "work", Member: "alpha", Branch: "work", Reason: "nothing was committed to it"}) {
		t.Errorf("deleted = %+v", res.BranchesDeleted)
	}
}

// TestRemovingASessionKeepsItsCommits covers work that was never pushed: it
// does not stop the removal, and can be taken up again.
func TestRemovingASessionKeepsItsCommits(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	tip := sha(t, filepath.Join(s.Dir, "alpha"), "HEAD")
	if m := fate(t, c, s, "alpha"); m.Unsafe() || m.Fate != BranchKept || !strings.Contains(m.Reason, "1 commit not merged, on no remote") {
		t.Errorf("alpha = %+v", m)
	}
	res, err := c.DeleteSession(s, DeleteOptions{DeleteBranches: true, Force: true})
	if err != nil || len(res.BranchesKept) != 1 || len(res.BranchesDeleted) != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if sha(t, srcs["alpha"], "refs/heads/"+s.S.Branch) != tip {
		t.Error("the branch must be kept as it was")
	}
	again, err := c.CreateSession(s.Pkg, "again", SessionOptions{Branch: s.S.Branch, ReuseBranch: true})
	if err != nil || sha(t, filepath.Join(again.Dir, "alpha"), "HEAD") != tip {
		t.Errorf("taking it up again: %v", err)
	}
}

func TestMergedBranchesGoOnlyWhenAsked(t *testing.T) {
	for _, how := range []string{MergedCommit, MergedSquash, MergedRebase} {
		for _, ask := range []bool{false, true} {
			c, env := newCore(t)
			s, srcs := workSession(t, c, env, "alpha")
			a := srcs["alpha"]
			commitIn(t, filepath.Join(s.Dir, "alpha"), "more.txt")
			if _, err := c.Push(s, PushOptions{}); err != nil {
				t.Fatal(err)
			}
			if m := fate(t, c, s, "alpha"); m.Fate != BranchKept || !strings.Contains(m.Reason, "2 commits not merged, on origin/") {
				t.Errorf("%s, pushed: %+v", how, m)
			}
			mergeOnRemote(t, a, s.S.Branch, how, true)
			testutil.Git(t, a, "fetch", "--quiet", "--prune")
			if m := fate(t, c, s, "alpha"); m.Fate != BranchMerged || m.Reason != "merged ("+how+") into origin/main" || m.Unsafe() {
				t.Errorf("%s: %+v", how, m)
			}
			res, err := c.DeleteSession(s, DeleteOptions{DeleteBranches: ask})
			if err != nil {
				t.Fatal(err)
			}
			if gitx.BranchExists(a, s.S.Branch) == ask {
				t.Errorf("%s, asked=%v: %+v", how, ask, res)
			}
		}
	}
}

func TestBranchesThatCannotBeToldMergedAreKept(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, wt := srcs["alpha"], filepath.Join(s.Dir, "alpha")
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	mergeOnRemote(t, a, s.S.Branch, MergedSquash, true)
	testutil.Git(t, a, "fetch", "--quiet", "--prune")
	commitIn(t, wt, "late.txt")
	m := fate(t, c, s, "alpha")
	if m.Fate != BranchKept || !strings.Contains(m.Reason, "was deleted") {
		t.Errorf("alpha = %+v", m)
	}
	if _, err := c.DeleteSession(s, DeleteOptions{DeleteBranches: true, Force: true}); err != nil || !gitx.BranchExists(a, s.S.Branch) {
		t.Errorf("a late commit must survive: %v", err)
	}
}

// TestBaseBranchIsNeverDeleted covers a session file that names the base.
func TestBaseBranchIsNeverDeleted(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("base", []string{a}, nil))
	s.S.SetMember("alpha", manifest.SessionMember{Branch: "main", Created: true})
	if m := fate(t, c, s, "alpha"); m.Fate != BranchKept {
		t.Errorf("alpha = %+v", m)
	}
}

func TestDetachedCommitsStillStopARemoval(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("det", []string{a}, nil))
	wt := filepath.Join(s.Dir, "alpha")
	testutil.Git(t, wt, "checkout", "--quiet", "--detach")
	commitIn(t, wt, "stray.txt")
	if _, err := c.DeleteSession(s, DeleteOptions{}); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "1 commit on no branch") {
		t.Errorf("err = %v", err)
	}
	testutil.Git(t, wt, "branch", "saved")
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Errorf("on a branch it is safe: %v", err)
	}
}
