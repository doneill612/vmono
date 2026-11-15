package core

import (
	"os"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
)

func TestCreateOverStaleWorktreeRecordsOfAHandDeletedPackage(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	p, err := c.Create(request("again", []string{a}, []string{ref}), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// rm -rf of the package leaves both worktrees registered in the source
	// repos, and the package branch checked out at a path that is gone.
	if err := os.RemoveAll(p.Dir); err != nil {
		t.Fatal(err)
	}
	if at, _ := gitx.BranchCheckedOutAt(a, "vmono/again"); at != "" {
		t.Fatalf("a stale worktree must not count as holding the branch: %s", at)
	}

	var created *Package
	evs := collect(c, func() {
		created, err = c.Create(request("again", []string{a}, []string{ref}), CreateOptions{ReuseBranch: true})
	})
	if err != nil {
		t.Fatalf("recreating over stale records must work: %v", err)
	}
	if br := gitx.CurrentBranch(created.MemberDir(created.M.Members[0])); br != "vmono/again" {
		t.Errorf("branch = %q", br)
	}
	pruned := 0
	for _, ev := range evs {
		if ev.Status == apply.StatusWarn && strings.Contains(ev.Message, "pruned stale worktree record") {
			pruned++
		}
	}
	if pruned != 2 {
		t.Errorf("want a warning for each of the two stale records, got %d: %+v", pruned, evs)
	}
	for _, src := range []string{a, ref} {
		if list, _ := gitx.WorktreeList(src); len(list) != 2 {
			t.Errorf("%s should have its own worktree plus the new one only: %+v", src, list)
		}
	}
}
