package core

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// status returns the session's progress and the status of the named member.
func status(t *testing.T, c *Core, s *Session, name string) (SessionProgress, MemberStatus) {
	t.Helper()
	st := c.Status(s)
	for _, m := range st.Members {
		if m.Name == name {
			return st.SessionProgress, m
		}
	}
	t.Fatalf("no member %q", name)
	return SessionProgress{}, MemberStatus{}
}

// TestStatusFollowsABranchFromEmptyToDiverged walks one branch through every
// state short of merged.
func TestStatusFollowsABranchFromEmptyToDiverged(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("walk", []string{a}, nil))
	wt, branch := filepath.Join(s.Dir, "alpha"), s.S.Branch
	check := func(when, publish, state, text string) MemberStatus {
		t.Helper()
		p, m := status(t, c, s, "alpha")
		if m.Publish != publish || p.State != state || !strings.Contains(m.PublishText(), text) {
			t.Errorf("%s: publish %q, state %q, text %q; want %q, %q, %q", when, m.Publish, p.State, m.PublishText(), publish, state, text)
		}
		return m
	}

	check("new", PublishEmpty, SessionInProgress, "no commits yet")
	commitIn(t, wt, "one.txt")
	m := check("committed", PublishLocal, SessionInProgress, "1 commit, not pushed")
	if m.OwnCommits != 1 || m.Unpushed != 1 || m.BuiltOn != "origin/main" || m.RemoteBranch != "" {
		t.Errorf("committed: %+v", m)
	}
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	m = check("pushed", PublishPushed, SessionPushed, "pushed to origin/"+branch)
	if m.OwnCommits != 1 || m.Unpushed != 0 {
		t.Errorf("pushed: %+v", m)
	}
	testutil.WriteFile(t, filepath.Join(wt, "wip.txt"), "x")
	if p, _ := status(t, c, s, "alpha"); p.State != SessionInProgress {
		t.Errorf("uncommitted work makes it %q", p.State)
	}
	testutil.Git(t, wt, "clean", "-fdq")
	pushFromElsewhere(t, a, branch, "theirs.txt", "theirs\n")
	testutil.Git(t, a, "fetch", "--quiet")
	check("behind", PublishBehind, SessionPushed, "1 commit behind")
	commitIn(t, wt, "two.txt")
	check("diverged", PublishDiverged, SessionInProgress, "1 ahead, 1 behind")
	testutil.Git(t, wt, "reset", "--quiet", "--hard", "origin/"+branch)
	commitIn(t, wt, "three.txt")
	check("unpushed", PublishUnpushed, SessionInProgress, "1 commit to push")
}

func TestStatusSaysHowABranchWasMerged(t *testing.T) {
	for _, how := range []string{MergedCommit, MergedSquash, MergedRebase} {
		for _, deleted := range []bool{false, true} {
			c, env := newCore(t)
			s, srcs := workSession(t, c, env, "alpha")
			a, wt := srcs["alpha"], filepath.Join(s.Dir, "alpha")
			commitIn(t, wt, "more.txt")
			if _, err := c.Push(s, PushOptions{}); err != nil {
				t.Fatal(err)
			}
			mergeOnRemote(t, a, s.S.Branch, how, deleted)
			testutil.Git(t, a, "fetch", "--quiet", "--prune")

			before := testutil.Git(t, a, "count-objects", "-v")
			p, m := status(t, c, s, "alpha")
			if m.Publish != PublishMerged || m.MergedBy != how || m.MergedInto != "origin/main" || p.State != SessionMerged || p.Label() != "merged ("+how+")" {
				t.Errorf("%s deleted=%v: %+v, %+v", how, deleted, p, m)
			}
			if after := testutil.Git(t, a, "count-objects", "-v"); after != before {
				t.Errorf("%s: status wrote to the source repo", how)
			}

			// Work after the merge is no longer merged.
			commitIn(t, wt, "late.txt")
			p, m = status(t, c, s, "alpha")
			want := PublishUnpushed
			if deleted {
				want = PublishDeleted
			}
			if m.Publish != want || p.State != SessionInProgress {
				t.Errorf("%s deleted=%v, late commit: %+v, %+v", how, deleted, p, m)
			}
			if deleted && (len(p.RemoteDeleted) != 1 || !strings.Contains(p.Label(), "remote branch deleted")) {
				t.Errorf("%s: label %q", how, p.Label())
			}
		}
	}
}

func TestSessionStateIgnoresMembersWithNothingOnThem(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("part", []string{a, b}, nil))
	if p := c.Status(s).SessionProgress; p.State != SessionInProgress {
		t.Errorf("a session nothing was done in is %q", p.State)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "one.txt")
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if p := c.Status(s).SessionProgress; p.State != SessionPushed {
		t.Errorf("with beta untouched: %q", p.State)
	}
	commitIn(t, filepath.Join(s.Dir, "beta"), "two.txt")
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	mergeOnRemote(t, a, s.S.Branch, MergedSquash, true)
	testutil.Git(t, a, "fetch", "--quiet", "--prune")
	if p := c.Status(s).SessionProgress; p.State != SessionPushed {
		t.Errorf("one merged, one only pushed: %q", p.State)
	}
	rows, err := c.ListSessions(s.Pkg, true)
	if err != nil || rows[0].State != SessionPushed || len(rows[0].MergedBy) != 1 {
		t.Errorf("list = %+v, %v", rows, err)
	}
	if show := c.Show(s.Pkg, s); show.State != SessionPushed || show.Sessions[0].State != SessionPushed {
		t.Errorf("show = %+v", show.SessionProgress)
	}
}

// TestStatusIgnoresABaseUpstream covers session branches made before vmono
// stopped git from setting origin/main as their upstream.
func TestStatusIgnoresABaseUpstream(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	testutil.Git(t, srcs["alpha"], "branch", "--set-upstream-to=origin/main", s.S.Branch)
	if _, m := status(t, c, s, "alpha"); m.Publish != PublishLocal || m.RemoteBranch != "" {
		t.Errorf("status = %+v", m)
	}
}
