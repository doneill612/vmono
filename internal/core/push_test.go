package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// remoteOf returns the path of the bare remote testutil gave the source name.
func remoteOf(env *testutil.Env, name string) string {
	return filepath.Join(env.Root, "remotes", name+".git")
}

// pushRow returns the result row for the named member.
func pushRow(t *testing.T, res PushResult, name string) MemberPush {
	t.Helper()
	for _, m := range res.Members {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no member %q in %+v", name, res.Members)
	return MemberPush{}
}

// workSession creates a package over the named sources with one session, and
// commits a file in every member.
func workSession(t *testing.T, c *Core, env *testutil.Env, names ...string) (*Session, map[string]string) {
	t.Helper()
	srcs := map[string]string{}
	var list []string
	for _, n := range names {
		srcs[n] = env.NewSource(t, n)
		list = append(list, srcs[n])
	}
	_, s := newWork(t, c, request("push", list, nil))
	for _, n := range names {
		commitIn(t, filepath.Join(s.Dir, n), "work.txt")
	}
	return s, srcs
}

// mergeOnRemote merges branch into main on the remote of src the way a pull
// request is, from a separate clone, and then deletes it there if asked.
func mergeOnRemote(t *testing.T, src, branch, how string, deleteIt bool) {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, src, "clone", "--quiet", testutil.Git(t, src, "remote", "get-url", "origin"), clone)
	switch how {
	case MergedCommit:
		testutil.Git(t, clone, "merge", "--quiet", "--no-ff", "--no-edit", "origin/"+branch)
	case MergedSquash:
		testutil.Git(t, clone, "merge", "--quiet", "--squash", "origin/"+branch)
		testutil.Git(t, clone, "commit", "--quiet", "-m", "squashed (#1)")
	case MergedRebase:
		commitIn(t, clone, "first.txt") // otherwise the copies would be the commits themselves
		testutil.Git(t, clone, "cherry-pick", "main..origin/"+branch)
	}
	testutil.Git(t, clone, "push", "--quiet", "origin", "main")
	if deleteIt {
		testutil.Git(t, clone, "push", "--quiet", "origin", "--delete", branch)
	}
}

// remoteBranches lists the branches a bare remote has.
func remoteBranches(t *testing.T, env *testutil.Env, name string) []string {
	t.Helper()
	return strings.Fields(testutil.Git(t, remoteOf(env, name), "for-each-ref", "--format=%(refname:short)", "refs/heads/"))
}

func TestPushSendsTheSessionBranchAndNothingElse(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, branch := srcs["alpha"], s.S.Branch
	main := sha(t, remoteOf(env, "alpha"), "refs/heads/main")

	res, err := c.Push(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := pushRow(t, res, "alpha")
	if m.Status != PushPushed || m.Branch != branch || m.Remote != "origin" || m.Dest != branch || m.Commits != 1 || !m.New || !m.SetsUpstream || len(m.Notes) != 0 {
		t.Errorf("row = %+v", m)
	}
	if got, want := sha(t, remoteOf(env, "alpha"), "refs/heads/"+branch), sha(t, a, branch); got != want {
		t.Errorf("the remote has %s, want %s", got, want)
	}
	if sha(t, remoteOf(env, "alpha"), "refs/heads/main") != main || len(remoteBranches(t, env, "alpha")) != 2 {
		t.Errorf("only the session branch may reach the remote: %v", remoteBranches(t, env, "alpha"))
	}
	if tr, ok := gitx.UpstreamOf(a, branch); !ok || tr.Remote != "origin" || tr.Branch != branch {
		t.Errorf("the pushed branch should now track origin: %+v, %v", tr, ok)
	}

	commitIn(t, filepath.Join(s.Dir, "alpha"), "more.txt")
	res, err = c.Push(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); err != nil || m.Status != PushPushed || m.Commits != 1 || m.New || m.SetsUpstream {
		t.Errorf("second push = %+v, %v", m, err)
	}
}

// TestPushNeverGoesToTheBaseBranch covers session branches made before vmono
// stopped git from setting origin/main as their upstream.
func TestPushNeverGoesToTheBaseBranch(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a, branch := srcs["alpha"], s.S.Branch
	main := sha(t, remoteOf(env, "alpha"), "refs/heads/main")
	testutil.Git(t, a, "branch", "--set-upstream-to=origin/main", branch)

	res, err := c.Push(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); err != nil || m.Status != PushPushed || m.Dest != branch || !m.SetsUpstream {
		t.Errorf("row = %+v, %v", m, err)
	}
	if sha(t, remoteOf(env, "alpha"), "refs/heads/main") != main {
		t.Fatal("main must not be pushed to")
	}

	s.S.SetMember("alpha", manifest.SessionMember{Branch: "main"})
	res, err = c.Push(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); !errors.Is(err, ErrRefused) || m.Status != PushBlocked || !strings.Contains(m.Reason, "never pushed to") {
		t.Errorf("a session on the base branch itself: %+v, %v", m, err)
	}
}

func TestPushSkipsMembersWithNoCommits(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("push", []string{a, b}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	res, err := c.Push(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, res, "beta"); m.Status != PushNothing {
		t.Errorf("beta = %+v", m)
	}
	if got := remoteBranches(t, env, "beta"); len(got) != 1 {
		t.Errorf("a branch with nothing on it must not be published: %v", got)
	}
}

func TestPushPointsOutUncommittedWorkAndGoesAhead(t *testing.T) {
	c, env := newCore(t)
	s, _ := workSession(t, c, env, "alpha")
	testutil.WriteFile(t, filepath.Join(s.Dir, "alpha", "forgotten.txt"), "x")

	res, err := c.Push(s, PushOptions{})
	m := pushRow(t, res, "alpha")
	if err != nil || m.Status != PushPushed || len(m.Notes) != 1 || !strings.Contains(m.Notes[0], "1 untracked") {
		t.Errorf("row = %+v, %v", m, err)
	}
}

func TestPushBlocksAWorktreeMidRebase(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	wt := filepath.Join(s.Dir, "alpha")
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "mine\n")
	testutil.Git(t, wt, "commit", "--quiet", "-am", "mine")
	testutil.WriteFile(t, filepath.Join(srcs["alpha"], "README.md"), "theirs\n")
	testutil.Git(t, srcs["alpha"], "commit", "--quiet", "-am", "theirs")
	if err := gitx.RebaseOnto(wt, "main", "origin/main"); err == nil {
		t.Fatal("the rebase should stop")
	}
	res, err := c.PlanPush(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); err != nil || m.Status != PushBlocked || !strings.Contains(m.Reason, "rebase") {
		t.Errorf("row = %+v, %v", m, err)
	}
}

func TestPushLeavesMergedWorkAlone(t *testing.T) {
	for _, how := range []string{MergedCommit, MergedSquash, MergedRebase} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s deleted=%v", how, deleted), func(t *testing.T) {
				c, env := newCore(t)
				s, srcs := workSession(t, c, env, "alpha")
				commitIn(t, filepath.Join(s.Dir, "alpha"), "more.txt")
				if _, err := c.Push(s, PushOptions{}); err != nil {
					t.Fatal(err)
				}
				mergeOnRemote(t, srcs["alpha"], s.S.Branch, how, deleted)
				testutil.Git(t, srcs["alpha"], "fetch", "--quiet", "--prune")
				before := remoteBranches(t, env, "alpha")

				res, err := c.Push(s, PushOptions{})
				m := pushRow(t, res, "alpha")
				if err != nil || m.Status != PushMerged || !strings.Contains(m.Reason, "vmono session rm") {
					t.Errorf("row = %+v, %v", m, err)
				}
				if how != MergedCommit && !strings.Contains(m.Reason, how+"-merged into origin/main") {
					t.Errorf("reason = %q", m.Reason)
				}
				if got := remoteBranches(t, env, "alpha"); len(got) != len(before) {
					t.Errorf("remote branches = %v, were %v", got, before)
				}
			})
		}
	}
}

// TestPushDoesNotRecreateADeletedBranch covers commits made after a squash
// merge, which no longer read as merged.
func TestPushDoesNotRecreateADeletedBranch(t *testing.T) {
	c, env := newCore(t)
	s, srcs := workSession(t, c, env, "alpha")
	a := srcs["alpha"]
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	mergeOnRemote(t, a, s.S.Branch, MergedSquash, true)
	testutil.Git(t, a, "fetch", "--quiet", "--prune")
	commitIn(t, filepath.Join(s.Dir, "alpha"), "late.txt")

	res, err := c.Push(s, PushOptions{})
	m := pushRow(t, res, "alpha")
	if !errors.Is(err, ErrRefused) || m.Status != PushBlocked || !strings.Contains(m.Reason, "was deleted") {
		t.Fatalf("row = %+v, %v", m, err)
	}
	if got := remoteBranches(t, env, "alpha"); len(got) != 1 {
		t.Errorf("the branch must not come back: %v", got)
	}

	testutil.Git(t, a, "branch", "--unset-upstream", s.S.Branch)
	res, err = c.Push(s, PushOptions{})
	if m := pushRow(t, res, "alpha"); err != nil || m.Status != PushPushed {
		t.Errorf("what the reason advises should work: %+v, %v", m, err)
	}
}

func TestPushSkipsMembersThatAreUpToDate(t *testing.T) {
	c, env := newCore(t)
	s, _ := workSession(t, c, env, "alpha")
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}

	res, err := c.Push(s, PushOptions{})
	if err != nil {
		t.Fatalf("pushing what is already pushed is not an error: %v", err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushNothing || m.Commits != 0 {
		t.Errorf("row = %+v", m)
	}
}

func TestPushDryRunChangesNothing(t *testing.T) {
	c, env := newCore(t)
	s, _ := workSession(t, c, env, "alpha")

	res, err := c.Push(s, PushOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushReady || m.Commits != 1 {
		t.Errorf("row = %+v", m)
	}
	if got := remoteBranches(t, env, "alpha"); len(got) != 1 {
		t.Errorf("a dry run must not push: %v", got)
	}
}

func TestPushMembersLimitsWhatIsPushed(t *testing.T) {
	c, env := newCore(t)
	s, _ := workSession(t, c, env, "alpha", "beta")

	res, err := c.Push(s, PushOptions{Members: []string{"beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Members) != 1 || res.Members[0].Name != "beta" {
		t.Fatalf("only beta was asked for: %+v", res.Members)
	}
	if len(remoteBranches(t, env, "alpha")) != 1 {
		t.Error("alpha was not asked for and must not be pushed")
	}
	if len(remoteBranches(t, env, "beta")) != 2 {
		t.Error("beta should have been pushed")
	}

	if _, err := c.Push(s, PushOptions{Members: []string{"nope"}}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("an unknown member must be named in the error, got %v", err)
	}
}

func TestPushRefusesAReferenceMember(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	_, s := newWork(t, c, request("push", []string{a}, []string{ref}))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	if _, err := c.Push(s, PushOptions{Members: []string{"contracts"}}); err == nil {
		t.Error("a reference member has no work of its own to push")
	}
	res, err := c.PlanPush(s, PushOptions{})
	if err != nil || len(res.Members) != 1 {
		t.Errorf("only active members are planned: %+v, %v", res.Members, err)
	}
}

func TestPushBlocksADivergedBranchButStillPushesTheOthers(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("push", []string{a, b}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	commitIn(t, filepath.Join(s.Dir, "beta"), "work.txt")

	if _, err := c.Push(s, PushOptions{Members: []string{"alpha"}}); err != nil {
		t.Fatal(err)
	}
	pushFromElsewhere(t, a, s.S.Branch, "theirs.txt", "theirs\n")
	testutil.Git(t, a, "fetch", "--quiet", "origin")
	commitIn(t, filepath.Join(s.Dir, "alpha"), "mine.txt")
	alphaRemote := sha(t, remoteOf(env, "alpha"), "refs/heads/"+s.S.Branch)

	res, err := c.Push(s, PushOptions{})
	var blocked *PushBlockedError
	if !errors.As(err, &blocked) || !errors.Is(err, ErrRefused) {
		t.Fatalf("want a PushBlockedError matching ErrRefused, got %v", err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushBlocked || !strings.Contains(m.Reason, "diverged") {
		t.Errorf("alpha = %+v", m)
	}
	if m := pushRow(t, res, "beta"); m.Status != PushPushed {
		t.Errorf("beta is independent of alpha and should push: %+v", m)
	}
	if sha(t, remoteOf(env, "alpha"), "refs/heads/"+s.S.Branch) != alphaRemote {
		t.Error("a diverged branch must be left alone, never forced")
	}
}

func TestPushReportsAMemberGitRefusedWithoutStoppingTheOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the rejecting hook is a shell script")
	}
	c, env := newCore(t)
	s, _ := workSession(t, c, env, "alpha", "beta")
	hook := filepath.Join(remoteOf(env, "alpha"), "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho no >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := c.Push(s, PushOptions{})
	if err == nil {
		t.Fatal("a refused push must be an error")
	}
	if errors.Is(err, ErrRefused) {
		t.Errorf("a push git refused is a failure, not a member that was blocked from trying: %v", err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushFailed || m.Reason == "" {
		t.Errorf("alpha = %+v", m)
	}
	if m := pushRow(t, res, "beta"); m.Status != PushPushed {
		t.Errorf("beta should still push: %+v", m)
	}
}

func TestPushBlocksABranchWithNoRemote(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("push", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	testutil.Git(t, a, "fetch", "--quiet", "origin")
	testutil.Git(t, a, "remote", "rename", "origin", "one")
	testutil.Git(t, a, "remote", "add", "two", remoteOf(env, "alpha"))
	if m := pushRow(t, mustPlan(t, c, s), "alpha"); m.Status != PushBlocked || !strings.Contains(m.Reason, "several remotes") {
		t.Errorf("several remotes: %+v", m)
	}
	testutil.Git(t, a, "remote", "remove", "one")
	testutil.Git(t, a, "remote", "remove", "two")

	res, err := c.PlanPush(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushBlocked || !strings.Contains(m.Reason, "no remote") {
		t.Errorf("row = %+v", m)
	}
}

// mustPlan plans a push of every member.
func mustPlan(t *testing.T, c *Core, s *Session) PushResult {
	t.Helper()
	res, err := c.PlanPush(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestPushFromARepoWithNoOriginHead covers a source whose origin/HEAD was never
// set, so the member's base falls back to what the source has checked out. That
// fallback once resolved to the literal "HEAD", which inside the session
// worktree is the session branch, so a session never had any commits.
func TestPushFromARepoWithNoOriginHead(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "remote", "set-head", "origin", "--delete")
	_, s := newWork(t, c, request("nohead", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "one.txt")
	commitIn(t, filepath.Join(s.Dir, "alpha"), "two.txt")
	if m := pushRow(t, mustPlan(t, c, s), "alpha"); m.Status != PushReady || m.Commits != 2 {
		t.Errorf("row = %+v", m)
	}
}
