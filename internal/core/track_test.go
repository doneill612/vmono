package core

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestTrackWorksOnARemoteBranch(t *testing.T) {
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
	if sha(t, wt, "HEAD") != sha(t, a, "refs/remotes/origin/feat/x") || gitx.CurrentBranch(wt) != "feat/x" {
		t.Error("the session should be on the remote branch")
	}
	if tr, ok := gitx.UpstreamOf(a, "feat/x"); !ok || tr.Ref != "refs/remotes/origin/feat/x" {
		t.Errorf("upstream = %+v, %v", tr, ok)
	}
	if got := gitx.CurrentBranch(filepath.Join(s.Dir, "beta")); got != "feat/x" {
		t.Errorf("a member whose remote lacks it gets a new branch of that name, not %q", got)
	}
	if _, m := status(t, c, s, "alpha"); m.Publish != PublishPushed {
		t.Errorf("status = %+v", m)
	}

	commitIn(t, wt, "mine.txt")
	res, err := c.Push(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); err != nil || m.Status != PushPushed || m.Commits != 1 || m.New {
		t.Errorf("push = %+v, %v", res, err)
	}
}

// TestTrackTakesUpAnExistingLocalBranch covers the branch having been here
// before, which is how a removed session is picked up again.
func TestTrackTakesUpAnExistingLocalBranch(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, branch := srcs["alpha"], s.S.Branch
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	pushFromElsewhere(t, a, branch, "theirs.txt", "theirs\n")
	again, err := c.CreateSession(s.Pkg, "again", SessionOptions{Track: branch})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(again.Dir, "alpha")
	if sha(t, wt, "HEAD") != sha(t, a, "refs/remotes/origin/"+branch) {
		t.Error("the local branch should be brought up to its remote copy")
	}
	if testutil.Git(t, wt, "status", "--porcelain") != "" || again.S.Members["alpha"].Created {
		t.Errorf("members = %+v", again.S.Members)
	}

	// One that has diverged stays where it is.
	commitIn(t, wt, "mine.txt")
	if _, err := c.DeleteSession(again, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	pushFromElsewhere(t, a, branch, "more.txt", "more\n")
	tip := sha(t, a, "refs/heads/"+branch)
	if again, err = c.CreateSession(s.Pkg, "third", SessionOptions{Track: branch}); err != nil {
		t.Fatal(err)
	}
	if sha(t, filepath.Join(again.Dir, "alpha"), "HEAD") != tip {
		t.Error("a diverged branch must not move")
	}
}

func TestTrackRollsBack(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/x")
	testutil.Git(t, a, "branch", "feat/x")
	tip := sha(t, a, "refs/heads/feat/x")
	pushFromElsewhere(t, a, "feat/x", "theirs.txt", "theirs\n")
	m := request("rb", []string{a}, nil)
	p := mustPackage(t, c, m)
	c.Cfg.Hooks = map[string]config.Hook{"post_create": {Command: "exit 1", OnFailure: "abort"}}
	if _, err := c.CreateSession(p, "work", SessionOptions{Track: "feat/x"}); err == nil {
		t.Fatal("the create should fail")
	}
	if got := sha(t, a, "refs/heads/feat/x"); got != tip {
		t.Errorf("a branch that was moved must be put back: %s, want %s", got, tip)
	}
}

func TestTrackRefusals(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	p := mustPackage(t, c, request("trk", []string{a, b}, nil))
	pushFromElsewhere(t, a, "feat/x", "theirs.txt", "theirs\n")

	for what, opts := range map[string]SessionOptions{
		"explicit": {TrackByMember: map[string]string{"alpha": "nope"}},
		"lacks it": {TrackByMember: map[string]string{"beta": "feat/x"}},
		"all":      {Track: "nope"},
		"HEAD":     {TrackByMember: map[string]string{"alpha": "HEAD"}},
	} {
		if _, err := c.CreateSession(p, "work", opts); !errors.Is(err, ErrNotFound) && what != "HEAD" || err == nil {
			t.Errorf("%s: want not found, got %v", what, err)
		}
	}
	for what, opts := range map[string]SessionOptions{
		"both all":       {Track: "feat/x", Branch: "other"},
		"both member":    {TrackByMember: map[string]string{"alpha": "feat/x"}, BranchByMember: map[string]string{"alpha": "other"}},
		"unknown member": {TrackByMember: map[string]string{"zeta": "feat/x"}},
		"the base":       {TrackByMember: map[string]string{"alpha": "main"}},
	} {
		if _, err := c.CreateSession(p, "work", opts); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	testutil.Git(t, a, "fetch", "--quiet")
	testutil.Git(t, a, "checkout", "--quiet", "feat/x")
	_, err := c.CreateSession(p, "work", SessionOptions{Track: "feat/x"})
	refused(t, p, "work", "checked out in the source", err, "already checked out at")
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
