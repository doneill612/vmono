package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// refSession starts a session named work of a package with the active member
// alpha and the reference member lib, checked out at lib's branch feat, which is
// on its remote too.
func refSession(t *testing.T, c *Core, env *testutil.Env) (p *Package, s *Session, lib, tip string) {
	t.Helper()
	a, lib := env.NewSource(t, "alpha"), env.NewSource(t, "lib")
	tip = featureBranch(t, lib, "feat")
	testutil.Git(t, lib, "push", "--quiet", "-u", "origin", "feat")
	p = mustPackage(t, c, request("refs", []string{a}, []string{lib}))
	if _, err := c.CreateSession(p, "work", SessionOptions{FromByMember: map[string]string{"lib": "feat"}}); err != nil {
		t.Fatal(err)
	}
	return p, reloadSession(t, c, p), lib, tip
}

// reloadSession reads the session named work from disk.
func reloadSession(t *testing.T, c *Core, p *Package) *Session {
	t.Helper()
	s, err := c.LoadSession(p, "work")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFromChecksAReferenceOutElsewhere(t *testing.T) {
	c, env := newCore(t)
	p, s, lib, tip := refSession(t, c, env)
	dir := filepath.Join(s.Dir, "_ref", "lib")
	if sha(t, dir, "HEAD") != tip {
		t.Error("lib should be at feat")
	}
	if m := s.S.Members["lib"]; m.Ref != "feat" || m.BaseTip != tip || m.From != "" || m.Created {
		t.Errorf("lib = %+v", m)
	}
	if _, m := status(t, c, s, "lib"); m.Ref != "feat" {
		t.Errorf("status says %q", m.Ref)
	}
	agents, err := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(agents), "| reference | `feat` |") {
		t.Errorf("AGENTS.md should name the session's ref: %v\n%s", err, agents)
	}
	ins, err := c.Inspect(s)
	if err != nil || ins.Members[1].Ref != "feat" {
		t.Errorf("inspect = %+v, %v", ins.Members, err)
	}

	// A worktree that has to be made again goes back to the same ref.
	if err := gitx.WorktreeRemove(lib, dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sync(s, SyncOptions{}); err != nil || sha(t, dir, "HEAD") != tip {
		t.Errorf("recreated elsewhere: %v", err)
	}

	// Another session is not affected, and neither is a reference member by what
	// is named for every member.
	other, err := c.CreateSession(p, "other", SessionOptions{})
	if err != nil || sha(t, filepath.Join(other.Dir, "_ref", "lib"), "HEAD") != sha(t, lib, "origin/main") {
		t.Errorf("other session: %v", err)
	}
	for what, tc := range map[string]struct {
		opts SessionOptions
		want string
	}{
		"all members": {SessionOptions{From: "feat"}, "no member"},
		"missing":     {SessionOptions{FromByMember: map[string]string{"lib": "nope"}}, "lib"},
		"branch":      {SessionOptions{BranchByMember: map[string]string{"lib": "x"}}, "--from lib=REF"},
		"track":       {SessionOptions{TrackByMember: map[string]string{"lib": "feat"}}, "--from lib=REF"},
	} {
		_, err := c.CreateSession(p, "again", tc.opts)
		refused(t, p, "again", what, err, tc.want)
	}
	if _, err := c.CreateSession(p, "again", SessionOptions{FromByMember: map[string]string{"lib": "nope"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestSyncRefsFollowsTheSessionsRef(t *testing.T) {
	c, env := newCore(t)
	p, s, lib, _ := refSession(t, c, env)
	dir := filepath.Join(s.Dir, "_ref", "lib")
	sync := func(want string) SyncResult {
		t.Helper()
		res, err := c.Sync(reloadSession(t, c, p), SyncOptions{Refs: true})
		if err != nil || sha(t, dir, "HEAD") != want {
			t.Fatalf("at %s, want %s: %+v, %v", sha(t, dir, "HEAD"), want, res, err)
		}
		return res
	}

	// Commits on the branch are followed, and recorded.
	testutil.Git(t, lib, "checkout", "--quiet", "feat")
	commitIn(t, lib, "more.txt")
	testutil.Git(t, lib, "push", "--quiet")
	testutil.Git(t, lib, "checkout", "--quiet", "main")
	tip := sha(t, lib, "feat")
	sync(tip)
	if m := reloadSession(t, c, p).S.Members["lib"]; m.Ref != "feat" || m.BaseTip != tip {
		t.Errorf("lib = %+v", m)
	}

	// A local branch left behind after its work merged is not followed.
	mergeOnRemote(t, lib, "feat", MergedSquash, true)
	sync(remoteMain(t, lib))
	if m := reloadSession(t, c, p).S.Members["lib"]; m.Ref != "" || m.BaseTip != "" {
		t.Errorf("lib follows the package again: %+v", m)
	}
}

// remoteMain returns the commit main is at on the remote of src.
func remoteMain(t *testing.T, src string) string {
	t.Helper()
	return strings.Fields(testutil.Git(t, src, "ls-remote", "origin", "refs/heads/main"))[0]
}

// TestReferenceWhoseBranchIsGone covers a branch deleted everywhere: the member
// is left alone until the package's ref has its work, and what was checked out,
// though on no branch now, never stops the session from being removed.
func TestReferenceWhoseBranchIsGone(t *testing.T) {
	c, env := newCore(t)
	p, s, lib, tip := refSession(t, c, env)
	dir := filepath.Join(s.Dir, "_ref", "lib")
	testutil.Git(t, lib, "push", "--quiet", "origin", ":feat")
	testutil.Git(t, lib, "branch", "--quiet", "-D", "feat")

	res, err := c.Sync(s, SyncOptions{Refs: true})
	if err != nil || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "feat is gone") || sha(t, dir, "HEAD") != tip {
		t.Fatalf("result = %+v, %v", res, err)
	}
	s = reloadSession(t, c, p)
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 0 {
		t.Errorf("nothing made here is lost: %+v", bad)
	}
	commitIn(t, dir, "mine.txt")
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 1 || bad[0].Unpushed != 1 {
		t.Errorf("a commit made here is: %+v", bad)
	}
	testutil.Git(t, dir, "checkout", "--quiet", "--detach", tip)
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Errorf("remove: %v", err)
	}
}

func TestSessionRefThroughEdits(t *testing.T) {
	c, env := newCore(t)
	p, s, lib, tip := refSession(t, c, env)
	edit := func(change func(*manifest.Member)) *Session {
		t.Helper()
		e := edited(p)
		change(&e.Members[1])
		if _, err := c.ApplyEdit(p, e, EditOptions{}); err != nil {
			t.Fatal(err)
		}
		return reloadSession(t, c, p)
	}

	// The package's ref changing leaves the session at its own.
	s = edit(func(m *manifest.Member) { m.Ref = "main" })
	if s.S.Members["lib"].Ref != "feat" || sha(t, filepath.Join(s.Dir, "_ref", "lib"), "HEAD") != tip {
		t.Errorf("lib = %+v", s.S.Members["lib"])
	}
	// A member that becomes active starts from its base, with no ref left over.
	s = edit(func(m *manifest.Member) { m.Mode, m.Path, m.Ref = manifest.ModeActive, "", "" })
	if m := s.S.Members["lib"]; m.Ref != "" || m.BaseTip != "" || !m.Created {
		t.Errorf("lib = %+v", m)
	}
	if sha(t, filepath.Join(s.Dir, "lib"), "HEAD") != sha(t, lib, "origin/main") {
		t.Error("an active member starts from its base")
	}
}
