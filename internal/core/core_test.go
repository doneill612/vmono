package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// newCore builds a Core against an isolated environment.
func newCore(t *testing.T) (*Core, *testutil.Env) {
	t.Helper()
	env := testutil.NewEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, env
}

// request builds a create manifest from active and reference source paths.
func request(name string, active, refs []string) *manifest.Manifest {
	m := &manifest.Manifest{Name: name, Purpose: "test purpose"}
	for _, s := range active {
		m.Members = append(m.Members, manifest.Member{Source: s, Mode: manifest.ModeActive, Role: "role of " + filepath.Base(s)})
	}
	for _, s := range refs {
		m.Members = append(m.Members, manifest.Member{Source: s, Mode: manifest.ModeReference})
	}
	return m
}

func TestCreateActiveAndReference(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	ref := env.NewSource(t, "contracts")
	testutil.WriteFile(t, filepath.Join(a, ".env.local"), "SECRET=1\n")
	testutil.WriteFile(t, filepath.Join(a, ".gitignore"), ".env.local\n")
	testutil.Git(t, a, "add", ".gitignore")
	testutil.Git(t, a, "commit", "--quiet", "-m", "ignore env")
	testutil.Git(t, a, "push", "--quiet")

	m := request("demo", []string{a, b}, []string{ref})
	m.Members[0].Copy = []string{".env.local"}
	p, err := c.Create(m, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"alpha/README.md", "beta/README.md", "_ref/contracts/README.md", "AGENTS.md", "CLAUDE.md", ".gitignore", ".vmono/manifest.toml", "alpha/.env.local"} {
		if _, err := os.Stat(filepath.Join(p.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if br := gitx.CurrentBranch(filepath.Join(p.Dir, "alpha")); br != "vmono/demo" {
		t.Errorf("alpha branch = %q", br)
	}
	if st, _ := gitx.StatusOf(filepath.Join(p.Dir, "_ref", "contracts")); !st.Detached {
		t.Error("reference member must be detached")
	}
	agents, _ := os.ReadFile(filepath.Join(p.Dir, "AGENTS.md"))
	if !strings.Contains(string(agents), "test purpose") || !strings.Contains(string(agents), "role of alpha") {
		t.Errorf("AGENTS.md missing content:\n%s", agents)
	}

	// Shell repo: has an initial commit, and ignores member dirs.
	if !gitx.HasCommits(p.Dir) {
		t.Error("shell repo needs an initial commit")
	}
	if out := testutil.Git(t, p.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("shell repo should be clean after create, got:\n%s", out)
	}
	if log := testutil.Git(t, p.Dir, "log", "--format=%s"); log != "vmono: create demo" {
		t.Errorf("commit subject = %q", log)
	}
	// The source repo's checked-out branch is untouched.
	if br := gitx.CurrentBranch(a); br != "main" {
		t.Errorf("source branch changed to %q", br)
	}

	if _, err := c.Load("demo"); err != nil {
		t.Errorf("Load: %v", err)
	}
	if sums, _ := c.List(true); len(sums) != 1 || sums[0].Members != 3 || sums[0].Dirty {
		t.Errorf("List = %+v", sums)
	}
	if _, err := c.Create(request("demo", []string{a}, nil), CreateOptions{}); err == nil {
		t.Error("creating an existing package must fail")
	}
}

func TestCreateRollbackOnFailure(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")

	// Make beta's worktree add fail by occupying its target path with a
	// non-empty directory after preflight passes: use a copy pattern that
	// escapes the repo, which fails after both worktrees exist.
	m := request("rb", []string{a, b}, nil)
	m.Members[1].Copy = []string{"../escape"}

	ch := make(chan apply.Event, 256)
	c.Events = ch
	_, err := c.Create(m, CreateOptions{})
	close(ch)
	var se *apply.StepError
	if !errors.As(err, &se) || se.Member != "beta" {
		t.Fatalf("want StepError for beta, got %v", err)
	}
	var sawUndone bool
	for ev := range ch {
		if ev.Status == apply.StatusUndone {
			sawUndone = true
		}
	}
	if !sawUndone {
		t.Error("expected undone events")
	}

	dir, _ := c.PackageDir("rb")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("package dir must be removed on rollback: %v", err)
	}
	for _, src := range []string{a, b} {
		if gitx.BranchExists(src, "vmono/rb") {
			t.Errorf("branch must be deleted in %s", src)
		}
		list, _ := gitx.WorktreeList(src)
		if len(list) != 1 {
			t.Errorf("stale worktree left in %s: %+v", src, list)
		}
	}
}

func TestBranchExistsRequiresReuse(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "branch", "vmono/again")

	_, err := c.Create(request("again", []string{a}, nil), CreateOptions{})
	var be *BranchExistsError
	if !errors.As(err, &be) {
		t.Fatalf("want BranchExistsError, got %v", err)
	}
	p, err := c.Create(request("again", []string{a}, nil), CreateOptions{ReuseBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if br := gitx.CurrentBranch(filepath.Join(p.Dir, "alpha")); br != "vmono/again" {
		t.Errorf("branch = %q", br)
	}
	// Rolling back must not delete a pre-existing branch.
	c2, _ := New(c.Cfg)
	m := request("again2", []string{a}, nil)
	m.Branch = "vmono/again2"
	testutil.Git(t, a, "branch", "vmono/again2")
	m.Members = append(m.Members, manifest.Member{Source: a, Mode: manifest.ModeActive, Name: "alpha-dup"})
	if _, err := c2.Create(m, CreateOptions{ReuseBranch: true}); err == nil {
		t.Error("two active members from one repo must be rejected")
	}
	if !gitx.BranchExists(a, "vmono/again2") {
		t.Error("pre-existing branch must survive a failed create")
	}
}

func TestDeleteRefusesLostWorkThenForce(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	p, err := c.Create(request("del", []string{a, b}, nil), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// alpha: untracked file. beta: unpushed commit.
	testutil.WriteFile(t, filepath.Join(p.Dir, "alpha", "scratch.txt"), "x")
	testutil.WriteFile(t, filepath.Join(p.Dir, "beta", "feature.txt"), "x")
	testutil.Git(t, filepath.Join(p.Dir, "beta"), "add", "-A")
	testutil.Git(t, filepath.Join(p.Dir, "beta"), "commit", "--quiet", "-m", "work")

	_, err = c.Delete(p, DeleteOptions{})
	var re *RefusedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &re) || len(re.Unsafe) != 2 {
		t.Fatalf("want RefusedError for both members, got %v", err)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Fatal("refused delete must leave the package intact")
	}

	res, err := c.Delete(p, DeleteOptions{Force: true, DeleteBranches: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Error("package dir should be gone")
	}
	// alpha's branch has no new commits -> merged -> deleted. beta's has an
	// unmerged commit -> kept even with force.
	if gitx.BranchExists(a, "vmono/del") {
		t.Error("merged branch should be deleted")
	}
	if !gitx.BranchExists(b, "vmono/del") {
		t.Error("unmerged branch must be kept")
	}
	if len(res.BranchesDeleted) != 1 || len(res.BranchesKept) != 1 {
		t.Errorf("result = %+v", res)
	}
	for _, src := range []string{a, b} {
		if list, _ := gitx.WorktreeList(src); len(list) != 1 {
			t.Errorf("worktree entries left in %s: %+v", src, list)
		}
	}
	if _, err := c.Load("del"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load after delete = %v", err)
	}
}

func TestDeleteCleanKeepsBranchesByDefault(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, err := c.Create(request("clean", []string{a}, nil), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if bad := unsafeOf(c.SafetyCheck(p)); len(bad) != 0 {
		t.Fatalf("fresh package should be safe: %+v", bad)
	}
	if _, err := c.Delete(p, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !gitx.BranchExists(a, "vmono/clean") {
		t.Error("branches are kept unless --delete-branches")
	}
}

func TestStatusAndMissingMember(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, err := c.Create(request("st", []string{a}, nil), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(p.Dir, "alpha", "README.md"), "changed")
	st := c.Status(p)
	if len(st.Members) != 1 || st.Members[0].Dirty != 1 || st.Members[0].Branch != "vmono/st" {
		t.Errorf("status = %+v", st)
	}
	if err := os.RemoveAll(filepath.Join(p.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(p); !st.Members[0].Missing {
		t.Error("removed worktree should be reported missing")
	}
}

func TestResolveRepoRemembersPaths(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	if _, err := c.ResolveRepo("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown name should be not-found, got %v", err)
	}
	r, err := c.ResolveRepo(a)
	if err != nil || r.Name != "alpha" {
		t.Fatalf("resolve path: %+v, %v", r, err)
	}
	repos, _ := c.Repos()
	if len(repos) != 1 {
		t.Errorf("path-added repo should be remembered: %+v", repos)
	}
	// And survive a fresh Core (persisted in the index).
	c2, _ := New(c.Cfg)
	if repos, _ := c2.Repos(); len(repos) != 1 {
		t.Errorf("index not persisted: %+v", repos)
	}
}
