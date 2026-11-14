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

// newPackage creates a package with one active member "alpha".
func newPackage(t *testing.T, c *Core, env *testutil.Env, name string) (*Package, string) {
	t.Helper()
	a := env.NewSource(t, "alpha")
	p, err := c.Create(request(name, []string{a}, nil), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return p, a
}

// subjects returns the shell repo's commit subjects, newest first.
func subjects(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Split(testutil.Git(t, dir, "log", "--format=%s"), "\n")
}

func TestDescribeRoleNoteRenderAndCommitAsAuthor(t *testing.T) {
	c, env := newCore(t)
	p, _ := newPackage(t, c, env, "ctx")
	c.Author = "agent:claude"

	purpose, context := "New purpose.", "Ticket PLAT-1."
	if err := c.Describe(p, DescribeOptions{Purpose: &purpose, Context: &context}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetRole(p, "alpha", "Owns\nthe thing"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddNote(p, "contracts v3 is frozen"); err != nil {
		t.Fatal(err)
	}

	agents, _ := os.ReadFile(filepath.Join(p.Dir, "AGENTS.md"))
	for _, want := range []string{"New purpose.", "Ticket PLAT-1.", "Owns the thing", "contracts v3 is frozen", "(agent:claude)"} {
		if !strings.Contains(string(agents), want) {
			t.Errorf("AGENTS.md missing %q", want)
		}
	}
	reloaded, err := c.Load("ctx")
	if err != nil || reloaded.M.Purpose != "New purpose." || reloaded.M.Notes[0].By != "agent:claude" {
		t.Fatalf("manifest not persisted: %+v, %v", reloaded, err)
	}

	log := subjects(t, p.Dir)
	want := []string{"vmono: note (agent:claude)", "vmono: role alpha (agent:claude)", "vmono: describe purpose+context (agent:claude)", "vmono: create ctx"}
	if strings.Join(log, "|") != strings.Join(want, "|") {
		t.Errorf("commit log = %v, want %v", log, want)
	}
	if out := testutil.Git(t, p.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("shell repo should be clean: %s", out)
	}

	if err := c.SetRole(p, "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown member = %v", err)
	}
	if err := c.AddNote(p, "  "); err == nil {
		t.Error("empty note must be rejected")
	}
	if err := c.Describe(p, DescribeOptions{}); err == nil {
		t.Error("describe with nothing to change must fail")
	}
}

func TestAutoCommitOff(t *testing.T) {
	c, env := newCore(t)
	p, _ := newPackage(t, c, env, "nocommit")
	c.Cfg.AutoCommit = false
	if err := c.AddNote(p, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := subjects(t, p.Dir); len(got) != 1 {
		t.Errorf("auto_commit=false must not commit: %v", got)
	}
	if out := testutil.Git(t, p.Dir, "status", "--porcelain"); out == "" {
		t.Error("changes should be left uncommitted")
	}
}

func TestAddMemberAndRemoveMember(t *testing.T) {
	c, env := newCore(t)
	p, a := newPackage(t, c, env, "grow")
	b := env.NewSource(t, "beta")
	ref := env.NewSource(t, "contracts")

	added, err := c.AddMember(p, AddOptions{Source: b, Role: "second"})
	if err != nil || added.Name != "beta" || added.Path != "beta" {
		t.Fatalf("AddMember = %+v, %v", added, err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "beta", "README.md")); err != nil {
		t.Error("worktree must exist immediately")
	}
	if _, err := c.AddMember(p, AddOptions{Source: ref, Reference: true, Role: "specs"}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := c.Load("grow")
	if len(reloaded.M.Members) != 3 || reloaded.M.Members[2].Path != "_ref/contracts" {
		t.Fatalf("manifest members = %+v", reloaded.M.Members)
	}
	if !strings.Contains(strings.Join(subjects(t, p.Dir), "|"), "vmono: add beta (user)") {
		t.Errorf("add not committed: %v", subjects(t, p.Dir))
	}
	ign, _ := os.ReadFile(filepath.Join(p.Dir, ".gitignore"))
	if !strings.Contains(string(ign), "/beta/") {
		t.Errorf(".gitignore not regenerated: %s", ign)
	}

	if _, err := c.AddMember(p, AddOptions{Source: a}); err == nil {
		t.Error("adding the same repo as a second active member must fail")
	}
	if _, err := c.AddMember(p, AddOptions{Source: b, Reference: true}); err == nil {
		t.Error("duplicate member name must fail")
	}

	// Remove: refused when dirty, allowed when forced, branch kept.
	testutil.WriteFile(t, filepath.Join(p.Dir, "beta", "wip.txt"), "x")
	_, err = c.RemoveMember(p, "beta", RemoveOptions{})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("dirty remove = %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "beta")); err != nil {
		t.Fatal("refused removal must leave the worktree")
	}
	res, err := c.RemoveMember(p, "beta", RemoveOptions{Force: true, DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.BranchesDeleted) != 1 || gitx.BranchExists(b, "vmono/grow") {
		t.Errorf("merged branch should be deleted: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "beta")); !os.IsNotExist(err) {
		t.Error("worktree should be gone")
	}
	if reloaded, _ = c.Load("grow"); len(reloaded.M.Members) != 2 {
		t.Errorf("member not dropped from manifest: %+v", reloaded.M.Members)
	}
	if _, err := c.RemoveMember(p, "ghost", RemoveOptions{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove unknown = %v", err)
	}
}

func TestAddMemberFailureLeavesManifestAlone(t *testing.T) {
	c, env := newCore(t)
	p, _ := newPackage(t, c, env, "safe")
	b := env.NewSource(t, "beta")

	_, err := c.AddMember(p, AddOptions{Source: b, Copy: []string{"../escape"}})
	if err == nil {
		t.Fatal("expected copy failure")
	}
	if len(p.M.Members) != 1 {
		t.Errorf("in-memory manifest changed: %+v", p.M.Members)
	}
	reloaded, _ := c.Load("safe")
	if len(reloaded.M.Members) != 1 {
		t.Errorf("on-disk manifest changed: %+v", reloaded.M.Members)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "beta")); !os.IsNotExist(err) {
		t.Error("failed add must be rolled back")
	}
	if gitx.BranchExists(b, "vmono/safe") {
		t.Error("created branch must be removed on rollback")
	}
	if list, _ := gitx.WorktreeList(b); len(list) != 1 {
		t.Errorf("stale worktree: %+v", list)
	}

	// Branch already exists in the new member's source.
	testutil.Git(t, b, "branch", "vmono/safe")
	var be *BranchExistsError
	if _, err := c.AddMember(p, AddOptions{Source: b}); !errors.As(err, &be) {
		t.Errorf("want BranchExistsError, got %v", err)
	}
	if _, err := c.AddMember(p, AddOptions{Source: b, ReuseBranch: true}); err != nil {
		t.Errorf("reuse should succeed: %v", err)
	}
}

func TestShow(t *testing.T) {
	c, env := newCore(t)
	p, _ := newPackage(t, c, env, "view")
	if err := c.AddNote(p, "first"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddNote(p, "second"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(p.Dir, "alpha", "README.md"), "changed")
	s := c.Show(p)
	if s.Name != "view" || s.Purpose != "test purpose" || len(s.Notes) != 2 || len(s.Members) != 1 {
		t.Fatalf("show = %+v", s)
	}
	if s.Notes[0].Text == s.Notes[1].Text {
		t.Error("notes should be distinct")
	}
	m := s.Members[0]
	if m.Dirty != 1 || m.Mode != manifest.ModeActive || m.Branch != "vmono/view" || m.Source == "" || m.Role == "" {
		t.Errorf("member = %+v", m)
	}
}
