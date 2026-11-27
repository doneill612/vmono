package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestSyncRecreatesMissingWorktreeAndWarnsOnUnlisted(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	_, s := newWork(t, c, request("heal", []string{a}, []string{ref}))

	// Simulate a deleted worktree directory (stale registration remains) and
	// a stray directory the manifest does not list.
	if err := os.RemoveAll(filepath.Join(s.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.Dir, "_ref", "contracts")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "_ref", "old-thing"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Also drop a generated file; sync must regenerate it.
	if err := os.Remove(filepath.Join(s.Dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	res, err := c.Sync(s, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Recreated, ",") != "alpha,contracts" {
		t.Errorf("recreated = %v", res.Recreated)
	}
	if strings.Join(res.Unlisted, ",") != "_ref/old-thing,scratch" {
		t.Errorf("unlisted = %v", res.Unlisted)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha", "README.md")); err != nil {
		t.Error("alpha not recreated")
	}
	if br := gitx.CurrentBranch(filepath.Join(s.Dir, "alpha")); br != "vmono/heal/work" {
		t.Errorf("recreated branch = %q (existing branch must be reused)", br)
	}
	if st, _ := gitx.StatusOf(filepath.Join(s.Dir, "_ref", "contracts")); !st.Detached {
		t.Error("reference not recreated detached")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "AGENTS.md")); err != nil {
		t.Error("AGENTS.md not regenerated")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "scratch")); err != nil {
		t.Error("sync must never remove unlisted directories")
	}

	// A second sync is a no-op.
	res, err = c.Sync(s, SyncOptions{})
	if err != nil || len(res.Recreated) != 0 || len(res.Files) != 0 {
		t.Errorf("second sync = %+v, %v", res, err)
	}
}

func TestSyncSkipsForeignDirectoryAndMissingSource(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	_, s := newWork(t, c, request("odd", []string{a, b}, nil))
	// alpha's directory becomes an unrelated repo; beta's source vanishes.
	if err := os.RemoveAll(filepath.Join(s.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Init(filepath.Join(s.Dir, "alpha"), "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	res, err := c.Sync(s, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 2 || len(res.Recreated) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestSyncRefsMovesCleanReferencesOnly(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	ref2 := env.NewSource(t, "specs")
	_, s := newWork(t, c, request("refs", []string{a}, []string{ref, ref2}))

	// Both sources get a new commit on origin/main.
	for _, src := range []string{ref, ref2} {
		testutil.WriteFile(t, filepath.Join(src, "new.txt"), "x")
		testutil.Git(t, src, "add", "-A")
		testutil.Git(t, src, "commit", "--quiet", "-m", "advance")
		testutil.Git(t, src, "push", "--quiet")
	}
	// A local change in one reference member blocks moving it.
	testutil.WriteFile(t, filepath.Join(s.Dir, "_ref", "specs", "scratch"), "x")

	before := testutil.Git(t, filepath.Join(s.Dir, "_ref", "contracts"), "rev-parse", "HEAD")
	if res, _ := c.Sync(s, SyncOptions{}); len(res.RefsMoved) != 0 {
		t.Fatalf("refs must not move without --refs: %+v", res)
	}
	res, err := c.Sync(s, SyncOptions{Refs: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.RefsMoved, ",") != "contracts" {
		t.Errorf("moved = %v, skipped = %v", res.RefsMoved, res.Skipped)
	}
	after := testutil.Git(t, filepath.Join(s.Dir, "_ref", "contracts"), "rev-parse", "HEAD")
	if before == after {
		t.Error("contracts HEAD should have advanced")
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "specs") {
		t.Errorf("dirty reference should be skipped: %v", res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "_ref", "contracts", "new.txt")); err != nil {
		t.Error("moved reference should contain the new commit's file")
	}
}
