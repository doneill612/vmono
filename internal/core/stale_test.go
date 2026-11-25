package core

import (
	"os"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
)

// countPruned counts the warnings that report a stale worktree record freed.
func countPruned(evs []apply.Event) int {
	n := 0
	for _, ev := range evs {
		if ev.Status == apply.StatusWarn && strings.Contains(ev.Message, "pruned stale worktree record") {
			n++
		}
	}
	return n
}

func TestCreateSessionOverStaleWorktreeRecordsOfAHandDeletedPackage(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	p, _ := newWork(t, c, request("again", []string{a}, []string{ref}))

	// rm -rf of the package leaves both worktrees registered in the source
	// repos, and the session branch checked out at a path that is gone.
	if err := os.RemoveAll(p.Dir); err != nil {
		t.Fatal(err)
	}
	if at, _ := gitx.BranchCheckedOutAt(a, "vmono/again"); at != "" {
		t.Fatalf("a stale worktree must not count as holding the branch: %s", at)
	}

	np := mustPackage(t, c, request("again", []string{a}, []string{ref}))
	var (
		created *Session
		err     error
	)
	evs := collect(c, func() {
		created, err = c.CreateSession(np, "work", SessionOptions{Branch: "vmono/again", ReuseBranch: true})
	})
	if err != nil {
		t.Fatalf("recreating over stale records must work: %v", err)
	}
	if br := gitx.CurrentBranch(created.MemberDir(created.Pkg.M.Members[0])); br != "vmono/again" {
		t.Errorf("branch = %q", br)
	}
	if pruned := countPruned(evs); pruned != 2 {
		t.Errorf("want a warning for each of the two stale records, got %d: %+v", pruned, evs)
	}
	for _, src := range []string{a, ref} {
		if list, _ := gitx.WorktreeList(src); len(list) != 2 {
			t.Errorf("%s should have its own worktree plus the new one only: %+v", src, list)
		}
	}
}

func TestCreateSessionOverStaleRecordsOfAHandDeletedSession(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("sess", []string{a}, nil))
	gone, err := c.CreateSession(p, "gone", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := c.CreateSession(p, "keep", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(gone.Dir); err != nil {
		t.Fatal(err)
	}
	var again *Session
	evs := collect(c, func() {
		again, err = c.CreateSession(p, "gone", SessionOptions{ReuseBranch: true})
	})
	if err != nil {
		t.Fatalf("recreating a hand-deleted session must work: %v", err)
	}
	if countPruned(evs) != 1 {
		t.Errorf("want one pruned record: %+v", evs)
	}
	if br := gitx.CurrentBranch(again.MemberDir(p.M.Members[0])); br != "gone" {
		t.Errorf("branch = %q", br)
	}
	if st, err := gitx.StatusOf(keep.MemberDir(p.M.Members[0])); err != nil || st.Branch != "keep" {
		t.Errorf("the sibling session must be untouched: %+v, %v", st, err)
	}
}
