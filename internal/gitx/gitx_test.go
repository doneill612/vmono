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

	if bare, err := IsBare(src); err != nil || bare {
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
	if n, err := Unpushed(wt); err != nil || n != 0 {
		t.Errorf("Unpushed clean = %d, %v", n, err)
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
	if n, _ := Unpushed(wt); n != 1 {
		t.Errorf("Unpushed after commit = %d, want 1", n)
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
	if ref != "HEAD" || !fell {
		t.Errorf("fallback = %q, %v", ref, fell)
	}
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
