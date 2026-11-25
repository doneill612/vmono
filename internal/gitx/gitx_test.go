package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestWorktreeLifecycleAndStatus(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")

	if _, bare, err := WorkTree(src); err != nil || bare {
		t.Fatalf("IsBare = %v, %v", bare, err)
	}
	ref, fell := ResolveDefaultRef(src, "origin/HEAD")
	if fell || ref != "origin/HEAD" {
		t.Fatalf("ResolveDefaultRef = %q, %v", ref, fell)
	}

	wt := filepath.Join(env.Root, "wt", "alpha")
	if err := WorktreeAddBranch(src, wt, "vmono/x", "origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	at, err := BranchCheckedOutAt(src, "vmono/x")
	if err != nil || at == "" {
		t.Fatalf("BranchCheckedOutAt = %q, %v", at, err)
	}
	if !BranchExists(src, "vmono/x") {
		t.Error("branch should exist")
	}

	st, err := StatusOf(wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch != "vmono/x" || st.Dirty != 0 || st.Untracked != 0 {
		t.Errorf("clean status wrong: %+v", st)
	}
	if n, err := OnNoBranch(wt); err != nil || n != 0 {
		t.Errorf("OnNoBranch clean = %d, %v", n, err)
	}

	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "changed\n")
	testutil.WriteFile(t, filepath.Join(wt, "new.txt"), "x")
	st, _ = StatusOf(wt)
	if st.Dirty != 1 || st.Untracked != 1 {
		t.Errorf("dirty status wrong: %+v", st)
	}

	if err := WorktreeRemove(src, wt, false); err == nil {
		t.Error("removing a dirty worktree without force must fail")
	}
	testutil.Git(t, wt, "add", "-A")
	testutil.Git(t, wt, "commit", "--quiet", "-m", "work")
	if n, _ := OnNoBranch(wt); n != 0 {
		t.Errorf("OnNoBranch after a commit on the branch = %d, want 0", n)
	}
	if IsMerged(src, "vmono/x", "origin/main") {
		t.Error("branch with new commit must not be merged")
	}
	if err := WorktreeRemove(src, wt, false); err != nil {
		t.Fatal(err)
	}
	if err := WorktreePrune(src); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBranch(src, "vmono/x", false); err == nil {
		t.Error("deleting an unmerged branch without force must fail")
	}
}

func TestDetachedAndCommonDir(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "beta")
	wt := filepath.Join(env.Root, "wt", "beta")
	if err := WorktreeAddDetached(src, wt, "origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	st, err := StatusOf(wt)
	if err != nil || !st.Detached {
		t.Fatalf("expected detached, got %+v, %v", st, err)
	}
	commitFile(t, wt, "stray.txt", "s\n", "stray")
	if n, err := OnNoBranch(wt); err != nil || n != 1 {
		t.Errorf("OnNoBranch of a detached commit = %d, %v", n, err)
	}
	cd, err := CommonDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(src, ".git"))
	got, _ := filepath.EvalSymlinks(cd)
	if got != want {
		t.Errorf("CommonDir = %s, want %s", got, want)
	}
}

func TestFallbackWithoutRemote(t *testing.T) {
	env := testutil.NewEnv(t)
	dir := filepath.Join(env.Root, "solo")
	if err := Init(dir, "main"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "f"), "x")
	if _, err := CommitAll(dir, "init", ""); err != nil {
		t.Fatal(err)
	}
	ref, fell := ResolveDefaultRef(dir, "origin/HEAD")
	if ref != "refs/heads/main" || !fell {
		t.Errorf("fallback = %q, %v", ref, fell)
	}
	testutil.Git(t, dir, "checkout", "--quiet", "--detach")
	ref, fell = ResolveDefaultRef(dir, "origin/HEAD")
	if want, _ := ResolveRef(dir, "HEAD"); ref != want || !fell {
		t.Errorf("detached fallback = %q, %v, want the commit %s", ref, fell, want)
	}
	testutil.Git(t, dir, "checkout", "--quiet", "main")
	if err := Fetch(dir); err != nil {
		t.Errorf("Fetch without remotes should be a no-op: %v", err)
	}
}

func TestCommitAllNoChangesAndIdentityFallback(t *testing.T) {
	env := testutil.NewEnv(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(env.Root, "empty"))
	if err := os.WriteFile(filepath.Join(env.Root, "empty"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(env.Root, "shell")
	if err := Init(dir, "main"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "a"), "1")
	ok, err := CommitAll(dir, "vmono: create", "")
	if err != nil || !ok {
		t.Fatalf("first commit = %v, %v", ok, err)
	}
	ok, err = CommitAll(dir, "vmono: noop", "")
	if err != nil || ok {
		t.Fatalf("second commit should be a no-op, got %v, %v", ok, err)
	}
}

// pushBranch publishes a new branch to name's remote from a throwaway
// clone, so a later fetch in the source has something to bring in.
func pushBranch(t *testing.T, env *testutil.Env, name, branch string) {
	t.Helper()
	remote := filepath.Join(env.Root, "remotes", name+".git")
	clone := filepath.Join(env.Root, "pusher", name+"-"+branch)
	testutil.Git(t, env.Root, "clone", "--quiet", remote, clone)
	testutil.Git(t, clone, "checkout", "--quiet", "-b", branch)
	testutil.WriteFile(t, filepath.Join(clone, branch+".txt"), branch)
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", branch)
	testutil.Git(t, clone, "push", "--quiet", "origin", branch)
}

func TestFetchManyDedupesAndKeepsGoingPastFailures(t *testing.T) {
	env := testutil.NewEnv(t)
	alpha := env.NewSource(t, "alpha")
	beta := env.NewSource(t, "beta")
	broken := env.NewSource(t, "broken")
	pushBranch(t, env, "alpha", "feat-a")
	pushBranch(t, env, "beta", "feat-b")
	testutil.Git(t, broken, "remote", "set-url", "origin", filepath.Join(env.Root, "remotes", "missing.git"))

	wt := filepath.Join(env.Root, "wt", "alpha")
	if err := WorktreeAddDetached(alpha, wt, "HEAD"); err != nil {
		t.Fatal(err)
	}

	res := FetchMany([]string{alpha, broken, wt, beta}, 2)
	if len(res) != 3 {
		t.Fatalf("got %d results, want 3 distinct repos: %+v", len(res), res)
	}
	if res[0].Repo != alpha || len(res[0].Paths) != 2 || res[0].Paths[1] != wt {
		t.Errorf("alpha and its worktree should be one result: %+v", res[0])
	}
	if res[1].Repo != broken || res[1].Err == nil {
		t.Errorf("broken remote should fail: %+v", res[1])
	}
	if res[2].Repo != beta || res[2].Err != nil {
		t.Errorf("beta should succeed: %+v", res[2])
	}
	if res[0].Err != nil {
		t.Errorf("alpha should succeed: %v", res[0].Err)
	}
	if !RefExists(alpha, "origin/feat-a") || !RefExists(beta, "origin/feat-b") {
		t.Error("successful repos should have fetched the new branches despite the failure")
	}
}

func TestFetchManyEmptyAndRemotelessRepos(t *testing.T) {
	env := testutil.NewEnv(t)
	if res := FetchMany(nil, 0); len(res) != 0 {
		t.Errorf("no paths should give no results: %+v", res)
	}
	solo := filepath.Join(env.Root, "solo")
	if err := Init(solo, "main"); err != nil {
		t.Fatal(err)
	}
	res := FetchMany([]string{solo}, 0)
	if len(res) != 1 || res[0].Err != nil {
		t.Errorf("a repo without remotes is a successful no-op: %+v", res)
	}
}

func TestIsAuthError(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", true},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", true},
		{"remote: Invalid username or password.\nfatal: Authentication failed for 'https://x/y.git/'", true},
		{"Host key verification failed.", true},
		{"fatal: '/nowhere' does not appear to be a git repository", false},
	}
	for _, c := range cases {
		err := &Error{Args: []string{"fetch"}, Stderr: c.stderr, Err: errors.New("exit status 128")}
		if got := IsAuthError(err); got != c.want {
			t.Errorf("IsAuthError(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
	if IsAuthError(errors.New("Permission denied (publickey)")) {
		t.Error("a non-git error must not be classified from its text")
	}
}

func TestNonInteractiveSSH(t *testing.T) {
	env := testutil.NewEnv(t)
	repo := env.NewSource(t, "alpha")
	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("GIT_SSH", "")

	got := nonInteractiveSSH(repo)
	if len(got) != 1 || !strings.Contains(got[0], "BatchMode=yes") {
		t.Fatalf("default should force batch mode: %v", got)
	}

	testutil.Git(t, repo, "config", "core.sshCommand", "ssh -i /custom")
	if got := nonInteractiveSSH(repo); got != nil {
		t.Errorf("core.sshCommand must be left alone: %v", got)
	}
	testutil.Git(t, repo, "config", "--unset", "core.sshCommand")

	t.Setenv("GIT_SSH_COMMAND", "ssh -F /mine")
	if got := nonInteractiveSSH(repo); got != nil {
		t.Errorf("GIT_SSH_COMMAND must be left alone: %v", got)
	}
	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("GIT_SSH", "/usr/bin/ssh")
	if got := nonInteractiveSSH(repo); got != nil {
		t.Errorf("GIT_SSH must be left alone: %v", got)
	}
}

func TestStaleWorktreesDoNotHoldBranchesAndPruneStaleReportsThem(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	wt := filepath.Join(env.Root, "wt", "alpha")
	if err := WorktreeAddBranch(src, wt, "vmono/x", "origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	if gone, err := PruneStale(src); err != nil || len(gone) != 0 {
		t.Fatalf("a live worktree is not stale: %v, %v", gone, err)
	}
	if at, _ := BranchCheckedOutAt(src, "vmono/x"); at == "" {
		t.Fatal("a live worktree holds its branch")
	}

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if at, err := BranchCheckedOutAt(src, "vmono/x"); err != nil || at != "" {
		t.Errorf("a worktree whose directory is gone holds nothing: %q, %v", at, err)
	}
	gone, err := PruneStale(src)
	if err != nil || len(gone) != 1 || !samePath(gone[0], wt) {
		t.Fatalf("PruneStale = %v, %v", gone, err)
	}
	if list, _ := WorktreeList(src); len(list) != 1 {
		t.Errorf("record should be gone: %+v", list)
	}
}

// samePath compares paths after resolving symlinks, since temp dirs may sit
// behind one.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(filepath.Dir(a))
	rb, errB := filepath.EvalSymlinks(filepath.Dir(b))
	return errA == nil && errB == nil && filepath.Join(ra, filepath.Base(a)) == filepath.Join(rb, filepath.Base(b))
}

func TestBranchRefConflict(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "gamma")

	if c, found := BranchRefConflict(src, "vmono/pkg/one"); found {
		t.Fatalf("no branches yet, got conflict %q", c)
	}

	testutil.Git(t, src, "branch", "vmono/pkg")
	if c, found := BranchRefConflict(src, "vmono/pkg/one"); !found || c != "vmono/pkg" {
		t.Errorf("parent branch should conflict, got %q, %v", c, found)
	}
	if _, found := BranchRefConflict(src, "vmono/pkg"); found {
		t.Error("an exact match is BranchExists' business, not a ref conflict")
	}
	if _, found := BranchRefConflict(src, "vmono/other/one"); found {
		t.Error("unrelated branch must not conflict")
	}

	testutil.Git(t, src, "branch", "-D", "vmono/pkg")
	testutil.Git(t, src, "branch", "vmono/pkg/deep/one")
	if c, found := BranchRefConflict(src, "vmono/pkg"); !found || c != "vmono/pkg/deep/one" {
		t.Errorf("child branch should conflict, got %q, %v", c, found)
	}
}

func TestWorktreeRepairAfterMove(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "delta")
	from := filepath.Join(env.Root, "wt", "delta")
	to := filepath.Join(env.Root, "moved", "delta")
	if err := WorktreeAddBranch(src, from, "vmono/x", "origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	if err := WorktreeRepair(src, to); err != nil {
		t.Fatalf("repair: %v", err)
	}
	list, err := WorktreeList(src)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range list {
		if w.Branch == "vmono/x" {
			found = true
			if w.Prunable {
				t.Error("repaired worktree must not be prunable")
			}
			if filepath.Base(filepath.Dir(w.Path)) != "moved" {
				t.Errorf("worktree still registered at %s", w.Path)
			}
		}
	}
	if !found {
		t.Fatalf("worktree for vmono/x missing from %+v", list)
	}
	if st, err := StatusOf(to); err != nil || st.Branch != "vmono/x" {
		t.Errorf("status of moved worktree = %+v, %v", st, err)
	}
}

// commitFile writes content to name inside dir and commits it.
func commitFile(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, name), content)
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", msg)
}

func TestRebaseOnto(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "gamma")
	wt := filepath.Join(env.Root, "wt", "gamma")
	if err := WorktreeAddBranch(src, wt, "vmono/x", "main"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, wt, "a.txt", "a\n", "session work")
	commitFile(t, src, "b.txt", "b\n", "main moved")

	fork, err := MergeBase(src, "vmono/x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := RevCount(src, "vmono/x", "--not", "main"); n != 1 {
		t.Fatalf("own commits before rebase = %d, want 1", n)
	}
	if err := RebaseOnto(wt, "main", fork); err != nil {
		t.Fatal(err)
	}
	if RebaseInProgress(wt) {
		t.Error("a finished rebase must not read as in progress")
	}
	if !IsMerged(src, "main", "vmono/x") {
		t.Fatal("main should be an ancestor of the rebased branch")
	}
	if n, _ := RevCount(src, "main..vmono/x"); n != 1 {
		t.Errorf("rebased branch is %d ahead of main, want 1", n)
	}
}

func TestRebaseOntoStopsOnConflict(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "delta")
	wt := filepath.Join(env.Root, "wt", "delta")
	if err := WorktreeAddBranch(src, wt, "vmono/x", "main"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, wt, "README.md", "session version\n", "session edit")
	commitFile(t, src, "README.md", "main version\n", "main edit")

	fork, _ := MergeBase(src, "vmono/x", "main")
	if err := RebaseOnto(wt, "main", fork); err == nil {
		t.Fatal("conflicting rebase must fail")
	}
	if !RebaseInProgress(wt) {
		t.Error("rebase should be left in progress")
	}
	files, err := ConflictedFiles(wt)
	if err != nil || len(files) != 1 || files[0] != "README.md" {
		t.Errorf("ConflictedFiles = %v, %v", files, err)
	}
	testutil.Git(t, wt, "rebase", "--abort")
	if RebaseInProgress(wt) {
		t.Error("aborted rebase must not read as in progress")
	}
}

func TestDefaultBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "zeta")
	if got := DefaultBranch(src); got != "main" {
		t.Errorf("DefaultBranch with origin/HEAD = %q", got)
	}
	testutil.Git(t, src, "remote", "remove", "origin")
	testutil.Git(t, src, "checkout", "--quiet", "-b", "dev")
	if got := DefaultBranch(src); got != "dev" {
		t.Errorf("DefaultBranch without a remote falls back to the checked-out branch, got %q", got)
	}
}

func TestUpstreamOfAndRemotes(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")

	tr, ok := UpstreamOf(src, "main")
	if !ok || tr.Remote != "origin" || tr.Branch != "main" || tr.Ref != "refs/remotes/origin/main" {
		t.Fatalf("UpstreamOf(main) = %+v, %v", tr, ok)
	}
	testutil.Git(t, src, "branch", "topic")
	if _, ok := UpstreamOf(src, "topic"); ok {
		t.Error("a branch with no upstream must report none")
	}
	testutil.Git(t, src, "branch", "--set-upstream-to=main", "topic")
	if _, ok := UpstreamOf(src, "topic"); ok {
		t.Error("a branch tracking a local branch must report none")
	}
	if _, ok := UpstreamOf(src, "missing"); ok {
		t.Error("a branch that does not exist must report none")
	}
	if got := Remotes(src); len(got) != 1 || got[0] != "origin" {
		t.Errorf("Remotes = %v", got)
	}
}

func TestPushNewBranchSetsUpstream(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	testutil.Git(t, src, "checkout", "--quiet", "-b", "feature")
	testutil.WriteFile(t, filepath.Join(src, "f.txt"), "f")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "--quiet", "-m", "feature work")
	testutil.Git(t, src, "checkout", "--quiet", "main")

	if err := Push(src, "origin", "feature", "feature", true); err != nil {
		t.Fatal(err)
	}
	tr, ok := UpstreamOf(src, "feature")
	if !ok || tr.Remote != "origin" || tr.Branch != "feature" {
		t.Fatalf("upstream after push = %+v, %v", tr, ok)
	}
	if n, _ := RevCount(src, "feature", "--not", tr.Ref); n != 0 {
		t.Errorf("feature should be on origin, %d commits are not", n)
	}
	if got := CurrentBranch(src); got != "main" {
		t.Errorf("push must not move the checked-out branch, on %q", got)
	}
}

func TestPushRefusesNonFastForward(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	other := filepath.Join(env.Root, "other")
	testutil.Git(t, env.Root, "clone", "--quiet", filepath.Join(env.Root, "remotes", "alpha.git"), other)
	testutil.WriteFile(t, filepath.Join(other, "o.txt"), "o")
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "theirs")
	testutil.Git(t, other, "push", "--quiet", "origin", "main")

	testutil.WriteFile(t, filepath.Join(src, "m.txt"), "m")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "--quiet", "-m", "mine")

	err := Push(src, "origin", "main", "main", false)
	if err == nil {
		t.Fatal("pushing a diverged branch must fail: pushes are never forced")
	}
	if !IsNonFastForward(err) {
		t.Errorf("want a non-fast-forward error, got %v", err)
	}
	if IsAuthError(err) {
		t.Errorf("a refused push is not an auth error: %v", err)
	}
	if IsNonFastForward(errors.New("boom")) {
		t.Error("a plain error is not a non-fast-forward")
	}
}

func TestNewBranchTracksNothing(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	if err := WorktreeAddBranch(src, filepath.Join(env.Root, "wt"), "feat", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if tr, ok := UpstreamOf(src, "feat"); ok {
		t.Errorf("a branch cut from origin/main must not track it: %+v", tr)
	}
}

func TestUnsetUpstream(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	if err := UnsetUpstream(src, "main"); err != nil {
		t.Fatal(err)
	}
	if _, ok := UpstreamOf(src, "main"); ok {
		t.Error("main should track nothing")
	}
	if err := UnsetUpstream(src, "main"); err == nil {
		t.Error("a branch with no upstream has none to unset")
	}
}

func TestReflog(t *testing.T) {
	src, wt := branchWithWork(t, "alpha")
	tip, _ := ResolveRef(wt, "HEAD")
	first, _ := ResolveRef(wt, "HEAD~1")
	log := Reflog(src, "refs/heads/feat")
	if len(log) != 3 || log[0].Commit != tip || log[1].Commit != first || log[0].Pushed() {
		t.Errorf("reflog = %v, want %s then %s then where it was cut", log, tip, first)
	}
	if err := Push(src, "origin", "feat", "feat", true); err != nil {
		t.Fatal(err)
	}
	if log := Reflog(src, "refs/remotes/origin/feat"); len(log) != 1 || log[0].Commit != tip || !log[0].Pushed() {
		t.Errorf("reflog of the pushed branch = %v", log)
	}
	if got := Reflog(src, "refs/heads/nope"); got != nil {
		t.Errorf("a missing ref has no reflog: %v", got)
	}
}
