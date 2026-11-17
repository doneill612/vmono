package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

// sha resolves ref in repo, failing the test when it does not exist.
func sha(t *testing.T, repo, ref string) string {
	t.Helper()
	out, err := gitx.ResolveRef(repo, ref)
	if err != nil {
		t.Fatalf("resolve %s in %s: %v", ref, repo, err)
	}
	return out
}

// member returns the result row for the named member.
func member(t *testing.T, res FinishResult, name string) MemberLanding {
	t.Helper()
	for _, m := range res.Members {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no member %q in %+v", name, res.Members)
	return MemberLanding{}
}

func TestFinishFastForwardsACheckedOutLandingBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("ff", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	before := sha(t, a, "main")

	res, err := c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := member(t, res, "alpha")
	if m.Status != LandLanded || m.Into != "main" || m.Via != ViaFastForward || m.Commits != 1 {
		t.Errorf("row = %+v", m)
	}
	if !res.Finished || res.Closed {
		t.Errorf("result = %+v", res)
	}
	if got := sha(t, a, "main"); got != sha(t, a, s.S.Branch) || got == before {
		t.Errorf("main = %s, want the session tip %s", got, sha(t, a, s.S.Branch))
	}
	if _, err := os.Stat(filepath.Join(a, "work.txt")); err != nil {
		t.Errorf("the source's working tree should have the landed file: %v", err)
	}
	if out := testutil.Git(t, a, "status", "--porcelain"); out != "" {
		t.Errorf("the source must stay clean: %q", out)
	}

	loaded, err := c.LoadSession(p, s.Name)
	if err != nil {
		t.Fatalf("a finished session stays listed and loadable: %v", err)
	}
	if loaded.S.Finished.IsZero() {
		t.Error("the finish time should be recorded")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha")); err != nil {
		t.Error("finish must leave the worktrees in place")
	}
}

func TestFinishUpdatesALandingBranchNothingHasCheckedOut(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	featureBranch(t, a, "feature")
	p := mustPackage(t, c, request("ref", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	main := sha(t, a, "main")

	res, err := c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); m.Status != LandLanded || m.Via != ViaRefUpdate {
		t.Errorf("row = %+v", m)
	}
	if got, want := sha(t, a, "feature"), sha(t, a, s.S.Branch); got != want {
		t.Errorf("feature = %s, want %s", got, want)
	}
	if sha(t, a, "main") != main || gitx.CurrentBranch(a) != "main" {
		t.Error("main and the source's checkout must not move")
	}
}

func TestFinishCreatesALandingBranchThatDoesNotExist(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("create", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{Into: "feature/new"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	res, err := c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); !m.Create || m.Status != LandLanded {
		t.Errorf("row = %+v", m)
	}
	if got, want := sha(t, a, "feature/new"), sha(t, a, s.S.Branch); got != want {
		t.Errorf("feature/new = %s, want %s", got, want)
	}
}

func TestFinishRefusesADirtyCheckedOutTargetAndMovesNothing(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("dirty-target", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	main := sha(t, a, "main")
	testutil.WriteFile(t, filepath.Join(a, "README.md"), "edited in the source\n")

	res, err := c.Finish(s, FinishOptions{})
	var blocked *FinishBlockedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &blocked) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if m := member(t, res, "alpha"); m.Status != LandBlocked || !strings.Contains(m.Reason, "uncommitted") {
		t.Errorf("row = %+v", m)
	}
	if sha(t, a, "main") != main {
		t.Error("a refused finish must not move main")
	}
	loaded, _ := c.LoadSession(p, s.Name)
	if !loaded.S.Finished.IsZero() {
		t.Error("a refused finish must not mark the session")
	}
}

func TestFinishRefusesUncommittedWorkInTheSession(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("dirty-session", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	testutil.WriteFile(t, filepath.Join(s.Dir, "alpha", "forgotten.txt"), "x")

	_, err := c.Finish(s, FinishOptions{})
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "1 untracked") {
		t.Fatalf("want a refusal naming the untracked file, got %v", err)
	}
}

func TestFinishSkipsMembersWithNoCommitsOfTheirOwn(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	_, s := newWork(t, c, request("skip", []string{a, b}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	betaMain := sha(t, b, "main")

	res, err := c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "beta"); m.Status != LandNothing || m.Commits != 0 {
		t.Errorf("beta = %+v", m)
	}
	if m := member(t, res, "alpha"); m.Status != LandLanded {
		t.Errorf("alpha = %+v", m)
	}
	if sha(t, b, "main") != betaMain {
		t.Error("an untouched member's main must not move")
	}
}

func TestFinishConflictStopsEverythingThenResumes(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	_, s := newWork(t, c, request("conflict", []string{a, b}, nil))
	alphaWT, betaWT := filepath.Join(s.Dir, "alpha"), filepath.Join(s.Dir, "beta")

	// alpha needs a clean rebase because main moved; beta's main moved on the
	// very file the session edited.
	commitIn(t, alphaWT, "a.txt")
	commitIn(t, a, "moved.txt")
	testutil.WriteFile(t, filepath.Join(betaWT, "README.md"), "session version\n")
	testutil.Git(t, betaWT, "commit", "--quiet", "-am", "session edit")
	testutil.WriteFile(t, filepath.Join(b, "README.md"), "main version\n")
	testutil.Git(t, b, "commit", "--quiet", "-am", "main edit")
	alphaMain, betaMain := sha(t, a, "main"), sha(t, b, "main")

	res, err := c.Finish(s, FinishOptions{})
	var blocked *FinishBlockedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &blocked) {
		t.Fatalf("want a conflict refusal, got %v", err)
	}
	if m := member(t, res, "beta"); m.Status != LandConflict || len(m.Conflicts) != 1 || m.Conflicts[0] != "README.md" {
		t.Errorf("beta = %+v", m)
	}
	if sha(t, a, "main") != alphaMain || sha(t, b, "main") != betaMain {
		t.Error("no landing branch may move while a member is conflicted")
	}
	if !gitx.RebaseInProgress(betaWT) {
		t.Error("the conflicted rebase must be left for the user to resolve")
	}
	if _, err := c.Finish(s, FinishOptions{}); !errors.Is(err, ErrRefused) {
		t.Errorf("finishing again before resolving must still refuse, got %v", err)
	}

	testutil.WriteFile(t, filepath.Join(betaWT, "README.md"), "resolved\n")
	testutil.Git(t, betaWT, "add", "README.md")
	testutil.Git(t, betaWT, "-c", "core.editor=true", "rebase", "--continue")

	res, err = c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatalf("finish after resolving: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if m := member(t, res, name); m.Status != LandLanded {
			t.Errorf("%s = %+v", name, m)
		}
	}
	if sha(t, a, "main") != sha(t, a, s.S.Branch) || sha(t, b, "main") != sha(t, b, s.S.Branch) {
		t.Error("both mains should be at their session tips")
	}
	if got := testutil.Git(t, b, "show", "main:README.md"); got != "resolved" {
		t.Errorf("beta README on main = %q", got)
	}
	if _, err := os.Stat(filepath.Join(a, "moved.txt")); err != nil {
		t.Errorf("alpha's own main commit must survive the rebase: %v", err)
	}
}

func TestFinishAgainLandsOnlyNewCommits(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("again", []string{a}, nil))
	wt := filepath.Join(s.Dir, "alpha")
	commitIn(t, wt, "one.txt")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}

	res, err := c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); m.Status != LandNothing {
		t.Errorf("a second finish with nothing new = %+v", m)
	}

	commitIn(t, wt, "two.txt")
	before, _ := gitx.RevCount(a, "origin/main..main")
	res, err = c.Finish(s, FinishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); m.Status != LandLanded || m.Commits != 1 {
		t.Errorf("only the new commit should land: %+v", m)
	}
	if after, _ := gitx.RevCount(a, "origin/main..main"); after != before+1 {
		t.Errorf("main gained %d commits, want 1", after-before)
	}
	loaded, _ := c.LoadSession(p, s.Name)
	if loaded.S.Finished.IsZero() {
		t.Error("the finish time should be refreshed")
	}
}

func TestFinishCloseRemovesTheSessionAndItsBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("close", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	res, err := c.Finish(s, FinishOptions{Close: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Closed || res.Delete == nil || len(res.Delete.BranchesDeleted) != 1 {
		t.Errorf("result = %+v", res)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Error("the session directory should be gone")
	}
	if gitx.BranchExists(a, s.S.Branch) {
		t.Error("the session branch is merged into main and should be deleted")
	}
	if _, err := os.Stat(filepath.Join(a, "work.txt")); err != nil {
		t.Errorf("the landed work must remain: %v", err)
	}
	if _, err := c.Load(p.Name); err != nil {
		t.Errorf("the package stays: %v", err)
	}
}

func TestFinishDryRunChangesNothing(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("dry", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")
	main := sha(t, a, "main")

	res, err := c.Finish(s, FinishOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); m.Status != LandReady || m.Commits != 1 {
		t.Errorf("row = %+v", m)
	}
	if res.Finished || sha(t, a, "main") != main {
		t.Error("a dry run must change nothing")
	}
	if loaded, _ := c.LoadSession(p, s.Name); !loaded.S.Finished.IsZero() {
		t.Error("a dry run must not mark the session")
	}
}

func TestFinishIntoOverrideIsRecorded(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("override", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	if _, err := c.Finish(s, FinishOptions{Into: "release"}); err != nil {
		t.Fatal(err)
	}
	if !gitx.BranchExists(a, "release") {
		t.Error("the override branch should be created")
	}
	loaded, _ := c.LoadSession(p, s.Name)
	if loaded.S.Into["alpha"] != "release" {
		t.Errorf("the override should be recorded, got %v", loaded.S.Into)
	}
	if bad := unsafeOf(c.SafetyCheck(loaded)); len(bad) != 0 {
		t.Errorf("work landed on the recorded branch must count as safe: %+v", bad)
	}
	if _, err := c.Finish(s, FinishOptions{Into: "bad name"}); err == nil {
		t.Error("an invalid override must be rejected")
	}
}

func TestSessionStateFollowsTheWork(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("state", []string{a}, nil))
	wt := filepath.Join(s.Dir, "alpha")

	stateOf := func() string {
		t.Helper()
		rows, err := c.ListSessions(p, false)
		if err != nil || len(rows) != 1 {
			t.Fatalf("ListSessions = %+v, %v", rows, err)
		}
		fresh, err := c.LoadSession(p, s.Name)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.SessionState(fresh); got != rows[0].State {
			t.Errorf("SessionState %q disagrees with the list row %q", got, rows[0].State)
		}
		return rows[0].State
	}

	if got := stateOf(); got != SessionInProgress {
		t.Errorf("a new session is %q", got)
	}
	commitIn(t, wt, "one.txt")
	st := c.Status(s)
	if m := st.Members[0]; m.Into != "main" || m.ToLand != 1 {
		t.Errorf("before finishing: into=%q to_land=%d", m.Into, m.ToLand)
	}

	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(); got != SessionFinished {
		t.Errorf("after finish the session is %q", got)
	}
	fresh, _ := c.LoadSession(p, s.Name)
	st = c.Status(fresh)
	if st.State != SessionFinished || st.FinishedAt == "" || st.Members[0].ToLand != 0 {
		t.Errorf("status after finish = %+v", st)
	}
	rows, _ := c.ListSessions(p, false)
	if rows[0].FinishedAt == "" {
		t.Error("the list row should carry the finish time")
	}

	// Relaunching and working again reopens the session without any command.
	commitIn(t, wt, "two.txt")
	if got := stateOf(); got != SessionInProgress {
		t.Errorf("a new commit leaves the session %q", got)
	}
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(); got != SessionFinished {
		t.Errorf("finishing again leaves the session %q", got)
	}
	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "edited\n")
	if got := stateOf(); got != SessionInProgress {
		t.Errorf("an uncommitted edit leaves the session %q", got)
	}
}

func TestMovingTheLandingBranchDoesNotReopenAFinishedSession(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("moved", []string{a}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "one.txt")
	if _, err := c.Finish(s, FinishOptions{}); err != nil {
		t.Fatal(err)
	}
	commitIn(t, a, "elsewhere.txt")

	fresh, _ := c.LoadSession(p, s.Name)
	if got := c.SessionState(fresh); got != SessionFinished {
		t.Errorf("state = %q; other work landing on main must not reopen it", got)
	}
}

func TestFinishOverridesOneMembersLandingBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	p, s := newWork(t, c, request("per-member", []string{a, b}, nil))
	commitIn(t, filepath.Join(s.Dir, "alpha"), "a.txt")
	commitIn(t, filepath.Join(s.Dir, "beta"), "b.txt")

	if _, err := c.Finish(s, FinishOptions{IntoByMember: map[string]string{"ghost": "x"}}); err == nil {
		t.Fatal("a member that is not in the package must be rejected")
	}
	res, err := c.Finish(s, FinishOptions{IntoByMember: map[string]string{"beta": "release"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := member(t, res, "alpha"); m.Into != "main" || m.Status != LandLanded {
		t.Errorf("alpha = %+v", m)
	}
	if m := member(t, res, "beta"); m.Into != "release" || !m.Create {
		t.Errorf("beta = %+v", m)
	}
	if !gitx.BranchExists(b, "release") || sha(t, b, "main") == sha(t, b, s.S.Branch) {
		t.Error("beta should land on release and leave main alone")
	}
	loaded, _ := c.LoadSession(p, s.Name)
	if loaded.S.Into["beta"] != "release" || loaded.S.Into["alpha"] != "main" {
		t.Errorf("recorded landing branches = %v", loaded.S.Into)
	}
}
