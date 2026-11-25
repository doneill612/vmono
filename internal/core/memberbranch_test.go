package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// TestMemberWithABranchOfItsOwn rebuilds a member's worktree on the branch the
// session records for it, and checks everything that names the branch.
func TestMemberWithABranchOfItsOwn(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	_, s := newWork(t, c, request("own", []string{a, b}, nil))
	s.S.SetMember("beta", manifest.SessionMember{Branch: "feat/x"})
	if err := manifest.SaveSession(s.Dir, s.S); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(s.Dir, "beta")
	testutil.Git(t, b, "worktree", "remove", wt)
	if res, err := c.Sync(s, SyncOptions{}); err != nil || len(res.Recreated) != 1 {
		t.Fatalf("sync = %+v, %v", res, err)
	}

	if got := gitx.CurrentBranch(wt); got != "feat/x" {
		t.Errorf("beta is on %q", got)
	}
	if got := gitx.CurrentBranch(filepath.Join(s.Dir, "alpha")); got != s.S.Branch {
		t.Errorf("alpha is on %q", got)
	}
	ins, err := c.Inspect(s)
	if err != nil || ins.Members[0].Branch != s.S.Branch || ins.Members[1].Branch != "feat/x" {
		t.Errorf("inspect = %+v, %v", ins.Members, err)
	}
	agents, err := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agents), "`feat/x`") || !strings.Contains(string(agents), "`"+s.S.Branch+"`") {
		t.Errorf("AGENTS.md should list both branches (%v):\n%s", err, agents)
	}
	if res, err := c.Update(s, UpdateOptions{}); err != nil {
		t.Errorf("update should accept the member's own branch: %+v, %v", res, err)
	}
}
