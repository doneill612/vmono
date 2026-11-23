package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
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

// finishedWork creates a package over the named sources with one session,
// commits a file in every member, and finishes it.
func finishedWork(t *testing.T, c *Core, env *testutil.Env, names ...string) (*Session, map[string]string) {
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
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	return s, srcs
}

func TestPushRefusesASessionThatIsNotFinished(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("push", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	for name, call := range map[string]func() error{
		"Push":     func() error { _, err := c.Push(s, PushOptions{}); return err },
		"PlanPush": func() error { _, err := c.PlanPush(s, PushOptions{}); return err },
	} {
		err := call()
		var nf *PushNotFinishedError
		if !errors.As(err, &nf) || !errors.Is(err, ErrRefused) {
			t.Errorf("%s of an unfinished session: want PushNotFinishedError matching ErrRefused, got %v", name, err)
		}
	}
}

func TestPushRefusesASessionWithNewWorkSinceItFinished(t *testing.T) {
	c, env := newCore(t)
	s, _ := finishedWork(t, c, env, "alpha")
	commitIn(t, filepath.Join(s.Dir, "alpha"), "more.txt")

	if _, err := c.Push(s, PushOptions{}); !errors.Is(err, ErrRefused) {
		t.Errorf("a session with commits that have not landed is in progress again: got %v", err)
	}
}

func TestPushSendsTheLandingBranchAndNothingElse(t *testing.T) {
	c, env := newCore(t)
	s, srcs := finishedWork(t, c, env, "alpha")
	a := srcs["alpha"]
	remote := remoteOf(env, "alpha")

	res, err := c.Push(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := pushRow(t, res, "alpha")
	if m.Status != PushPushed || m.Branch != "main" || m.Remote != "origin" || m.Dest != "main" || m.Commits != 1 || m.New || m.SetsUpstream {
		t.Errorf("row = %+v", m)
	}
	if got, want := sha(t, remote, "refs/heads/main"), sha(t, a, "main"); got != want {
		t.Errorf("origin/main = %s, want the landed tip %s", got, want)
	}
	if out := testutil.Git(t, remote, "for-each-ref", "--format=%(refname)", "refs/heads/"); out != "refs/heads/main" {
		t.Errorf("only the landing branch may reach the remote, it has:\n%s", out)
	}
	if st := c.Status(s); st.State != SessionFinished {
		t.Errorf("a pushed session stays finished, got %q", st.State)
	}
}

func TestPushCreatesALandingBranchThatIsNewOnTheRemoteAndTracksIt(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("push", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature/new"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}

	plan, err := c.PlanPush(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, plan, "alpha"); m.Status != PushReady || !m.New || !m.SetsUpstream || m.Dest != "feature/new" {
		t.Fatalf("plan = %+v", m)
	}
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, want := sha(t, remoteOf(env, "alpha"), "refs/heads/feature/new"), sha(t, a, "feature/new"); got != want {
		t.Errorf("origin feature/new = %s, want %s", got, want)
	}
	if tr, ok := gitx.UpstreamOf(a, "feature/new"); !ok || tr.Remote != "origin" || tr.Branch != "feature/new" {
		t.Errorf("the pushed branch should now track origin: %+v, %v", tr, ok)
	}
}

func TestPushSkipsMembersThatAreUpToDate(t *testing.T) {
	c, env := newCore(t)
	s, _ := finishedWork(t, c, env, "alpha")
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
	s, _ := finishedWork(t, c, env, "alpha")
	remote := remoteOf(env, "alpha")
	before := sha(t, remote, "refs/heads/main")

	res, err := c.Push(s, PushOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushReady || m.Commits != 1 {
		t.Errorf("row = %+v", m)
	}
	if sha(t, remote, "refs/heads/main") != before {
		t.Error("a dry run must not push")
	}
}

func TestPushMembersLimitsWhatIsPushed(t *testing.T) {
	c, env := newCore(t)
	s, _ := finishedWork(t, c, env, "alpha", "beta")
	alphaBefore, betaBefore := sha(t, remoteOf(env, "alpha"), "refs/heads/main"), sha(t, remoteOf(env, "beta"), "refs/heads/main")

	res, err := c.Push(s, PushOptions{Members: []string{"beta"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Members) != 1 || res.Members[0].Name != "beta" {
		t.Fatalf("only beta was asked for: %+v", res.Members)
	}
	if sha(t, remoteOf(env, "alpha"), "refs/heads/main") != alphaBefore {
		t.Error("alpha was not asked for and must not be pushed")
	}
	if sha(t, remoteOf(env, "beta"), "refs/heads/main") == betaBefore {
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
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}

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

	other := filepath.Join(env.Root, "other")
	testutil.Git(t, env.Root, "clone", "--quiet", remoteOf(env, "alpha"), other)
	commitIn(t, other, "theirs.txt")
	testutil.Git(t, other, "push", "--quiet", "origin", "main")
	testutil.Git(t, a, "fetch", "--quiet", "origin")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	alphaRemote := sha(t, remoteOf(env, "alpha"), "refs/heads/main")

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
	if sha(t, remoteOf(env, "alpha"), "refs/heads/main") != alphaRemote {
		t.Error("a diverged branch must be left alone, never forced")
	}
}

func TestPushReportsAMemberGitRefusedWithoutStoppingTheOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the rejecting hook is a shell script")
	}
	c, env := newCore(t)
	s, _ := finishedWork(t, c, env, "alpha", "beta")
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
	testutil.Git(t, a, "branch", "--unset-upstream", "main")
	_, s := newWork(t, c, request("push", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, a, "remote", "remove", "origin")

	res, err := c.PlanPush(s, PushOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := pushRow(t, res, "alpha"); m.Status != PushBlocked || !strings.Contains(m.Reason, "no remote") {
		t.Errorf("row = %+v", m)
	}
}

func TestStatusCountsCommitsLeftToPush(t *testing.T) {
	c, env := newCore(t)
	s, _ := finishedWork(t, c, env, "alpha")

	st := c.Status(s)
	if m := st.Members[0]; m.IntoRemote != "origin/main" || m.IntoUnpushed != 1 {
		t.Errorf("before the push: %+v", m)
	}
	if _, err := c.Push(s, PushOptions{}); err != nil {
		t.Fatal(err)
	}
	if m := c.Status(s).Members[0]; m.IntoRemote != "origin/main" || m.IntoUnpushed != 0 {
		t.Errorf("after the push: %+v", m)
	}
}
