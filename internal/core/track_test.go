package core

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestTrackStartsFromAndLandsOnARemoteBranch(t *testing.T) {
	c, env := newCore(t)
	c.Cfg.FetchOnCreate = false // tracking fetches regardless
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	p := mustPackage(t, c, request("trk", []string{a, b}, nil))
	pushFromElsewhere(t, a, "feat/x", "theirs.txt", "theirs\n")

	s, err := c.CreateSession(p, "work", SessionOptions{Track: "feat/x"})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(s.Dir, "alpha")
	if sha(t, wt, "HEAD") != sha(t, a, "refs/remotes/origin/feat/x") {
		t.Error("the session should start at the remote branch")
	}
	if s.S.Into["alpha"] != "feat/x" || s.S.Into["beta"] != "main" {
		t.Errorf("into = %v", s.S.Into)
	}
	if gitx.BranchExists(a, "feat/x") {
		t.Error("the local branch is made at finish, not at create")
	}

	commitIn(t, wt, "work.txt")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	if tr, ok := gitx.UpstreamOf(a, "feat/x"); !ok || tr.Ref != "refs/remotes/origin/feat/x" {
		t.Errorf("upstream = %+v, %v", tr, ok)
	}
	push, err := c.Push(s, PushOptions{})
	if err != nil || push.Members[0].Status != PushPushed || push.Members[0].Commits != 1 || push.Members[0].New {
		t.Errorf("push = %+v, %v", push, err)
	}
}

func TestTrackRefusals(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("trk", []string{a}, nil))
	pushFromElsewhere(t, a, "feat/x", "theirs.txt", "theirs\n")

	for name, opts := range map[string]SessionOptions{
		"explicit": {TrackByMember: map[string]string{"alpha": "nope"}},
		"all":      {Track: "nope"},
		"HEAD":     {Track: "HEAD"},
	} {
		if _, err := c.CreateSession(p, "work", opts); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: want not found, got %v", name, err)
		}
	}
	for name, opts := range map[string]SessionOptions{
		"both all":       {Track: "feat/x", Into: "main"},
		"both member":    {TrackByMember: map[string]string{"alpha": "feat/x"}, IntoByMember: map[string]string{"alpha": "main"}},
		"unknown member": {TrackByMember: map[string]string{"zeta": "feat/x"}},
	} {
		if _, err := c.CreateSession(p, "work", opts); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := c.LoadSession(p, "work"); err == nil {
		t.Error("a refused session must leave nothing behind")
	}
}

func TestSessionStartsFromTheFresherCopyOfItsLandingBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	featureBranch(t, a, "feature")
	testutil.Git(t, a, "push", "--quiet", "-u", "origin", "feature")
	pushFromElsewhere(t, a, "feature", "theirs.txt", "theirs\n")
	p := mustPackage(t, c, request("stale", []string{a}, nil))

	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if sha(t, filepath.Join(s.Dir, "alpha"), "HEAD") != sha(t, a, "refs/remotes/origin/feature") {
		t.Error("a stale local branch must not win over its remote copy")
	}
	if n := toLand(t, c, s, "alpha"); n != 0 {
		t.Errorf("to land = %d, want 0", n)
	}
}

func TestTrackFallsBackToFetchedRefsWhenOffline(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("off", []string{a}, nil))
	pushFromElsewhere(t, a, "feat/x", "theirs.txt", "theirs\n")
	testutil.Git(t, a, "fetch", "--quiet")
	testutil.Git(t, a, "remote", "set-url", "origin", filepath.Join(env.Root, "gone.git"))

	if _, err := c.CreateSession(p, "work", SessionOptions{Track: "feat/x"}); err != nil {
		t.Errorf("a failed fetch should fall back to the refs on disk: %v", err)
	}
}
