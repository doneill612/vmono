package gitx

import (
	"os"
	"path/filepath"
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
