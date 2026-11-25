package gitx

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// branchWithWork makes a worktree of a new source on the branch feat with two
// commits, and returns both.
func branchWithWork(t *testing.T, name string) (src, wt string) {
	t.Helper()
	env := testutil.NewEnv(t)
	src = env.NewSource(t, name)
	wt = filepath.Join(env.Root, "wt", name)
	if err := WorktreeAddBranch(src, wt, "feat", "main"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, wt, "a.txt", "a\n", "a")
	commitFile(t, wt, "b.txt", "b\n", "b")
	return src, wt
}

// squashOnto lands branch on the checked-out branch of src as one commit.
func squashOnto(t *testing.T, src, branch string) {
	t.Helper()
	testutil.Git(t, src, "merge", "--squash", "--quiet", branch)
	testutil.Git(t, src, "commit", "--quiet", "-m", "squashed")
}

func TestSquashMerged(t *testing.T) {
	t.Run("not merged", func(t *testing.T) {
		src, _ := branchWithWork(t, "alpha")
		commitFile(t, src, "other.txt", "o\n", "other")
		if SquashMerged(src, "feat", "main") || RebaseMerged(src, "feat", "main") {
			t.Error("the work is nowhere on main")
		}
	})
	t.Run("squashed", func(t *testing.T) {
		src, wt := branchWithWork(t, "alpha")
		commitFile(t, src, "other.txt", "o\n", "other")
		squashOnto(t, src, "feat")
		commitFile(t, src, "later.txt", "l\n", "later")
		for _, repo := range []string{src, wt} {
			if !SquashMerged(repo, "feat", "main") {
				t.Errorf("should be found from %s", repo)
			}
		}
		if RebaseMerged(src, "feat", "main") {
			t.Error("a squash of two commits is not a rebase merge")
		}
	})
	t.Run("base merged into the branch first", func(t *testing.T) {
		src, wt := branchWithWork(t, "alpha")
		commitFile(t, src, "other.txt", "o\n", "other")
		testutil.Git(t, wt, "merge", "--quiet", "--no-edit", "main")
		commitFile(t, wt, "c.txt", "c\n", "c")
		squashOnto(t, src, "feat")
		if !SquashMerged(src, "feat", "main") {
			t.Error("what was merged in from main is not the branch's own change")
		}
	})
	t.Run("commit after the squash", func(t *testing.T) {
		src, wt := branchWithWork(t, "alpha")
		squashOnto(t, src, "feat")
		commitFile(t, wt, "late.txt", "l\n", "late")
		if SquashMerged(src, "feat", "main") {
			t.Error("the late commit is not on main")
		}
	})
	t.Run("edited squash", func(t *testing.T) {
		src, _ := branchWithWork(t, "alpha")
		testutil.Git(t, src, "merge", "--squash", "--quiet", "feat")
		commitFile(t, src, "a.txt", "edited\n", "squashed, edited")
		if SquashMerged(src, "feat", "main") {
			t.Error("the change on main is a different one")
		}
	})
	t.Run("no change of its own", func(t *testing.T) {
		src, wt := branchWithWork(t, "alpha")
		testutil.Git(t, wt, "revert", "--no-edit", "HEAD", "HEAD~1")
		if SquashMerged(src, "feat", "main") {
			t.Error("there is no change to find")
		}
	})
}

func TestSquashMergedOnlyReads(t *testing.T) {
	src, _ := branchWithWork(t, "alpha")
	commitFile(t, src, "other.txt", "o\n", "other")
	squashOnto(t, src, "feat")
	before := testutil.Git(t, src, "count-objects", "-v")
	if !SquashMerged(src, "feat", "main") {
		t.Fatal("should be merged")
	}
	if after := testutil.Git(t, src, "count-objects", "-v"); after != before {
		t.Errorf("objects were written to the repo:\n%s\n%s", before, after)
	}
}

func TestSquashMergedWithTheListSeparatorInThePath(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "a;b")
	commitFile(t, src, "x.txt", "x\n", "x")
	testutil.Git(t, src, "checkout", "--quiet", "-b", "feat", "HEAD~1")
	commitFile(t, src, "a.txt", "a\n", "a")
	commitFile(t, src, "b.txt", "b\n", "b")
	testutil.Git(t, src, "checkout", "--quiet", "main")
	squashOnto(t, src, "feat")
	if !SquashMerged(src, "feat", "main") {
		t.Error("should be merged")
	}
}

func TestRebaseMerged(t *testing.T) {
	src, wt := branchWithWork(t, "alpha")
	commitFile(t, src, "other.txt", "o\n", "other")
	testutil.Git(t, src, "cherry-pick", "feat~1")
	if RebaseMerged(src, "feat", "main") {
		t.Error("one of the two commits is missing")
	}
	testutil.Git(t, src, "cherry-pick", "feat")
	if !RebaseMerged(src, "feat", "main") {
		t.Error("both commits are on main")
	}
	commitFile(t, wt, "late.txt", "l\n", "late")
	if RebaseMerged(src, "feat", "main") {
		t.Error("the late commit is not on main")
	}
	if RebaseMerged(src, "main", "main") {
		t.Error("a branch with nothing of its own is not rebase merged")
	}
}
