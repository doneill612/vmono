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

// newWork creates a package from m and one session named "work" on branch
// vmono/<package>, the single-state shape most tests need. Tests of the
// default branch and of several sessions call CreateSession themselves.
func newWork(t *testing.T, c *Core, m *manifest.Manifest) (*Package, *Session) {
	t.Helper()
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.CreateSession(p, "work", SessionOptions{Branch: "vmono/" + m.Name})
	if err != nil {
		t.Fatal(err)
	}
	return p, s
}

// mustPackage creates a package and fails the test if it cannot.
func mustPackage(t *testing.T, c *Core, m *manifest.Manifest) *Package {
	t.Helper()
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreatePackageMakesNoWorktrees(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")

	p, err := c.CreatePackage(request("demo", []string{a}, []string{ref}))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".vmono/manifest.toml", ".gitignore", ".git"} {
		if _, err := os.Stat(filepath.Join(p.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	for _, rel := range []string{"alpha", "_ref", "AGENTS.md", "CLAUDE.md", ".claude"} {
		if _, err := os.Stat(filepath.Join(p.Dir, rel)); err == nil {
			t.Errorf("a package directory must not hold %s; that belongs to a session", rel)
		}
	}
	if log := testutil.Git(t, p.Dir, "log", "--format=%s"); log != "vmono: create demo" {
		t.Errorf("commit subject = %q", log)
	}
	if out := testutil.Git(t, p.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("package repo should be clean after create, got:\n%s", out)
	}
	if list, _ := gitx.WorktreeList(a); len(list) != 1 {
		t.Errorf("creating a package must not add worktrees: %+v", list)
	}
	if names, _ := p.Sessions(); len(names) != 0 {
		t.Errorf("new package has sessions: %v", names)
	}
	if _, err := c.CreatePackage(request("demo", []string{a}, nil)); err == nil {
		t.Error("creating an existing package must fail")
	}
	dup := request("dup", []string{a}, nil)
	dup.Members = append(dup.Members, manifest.Member{Source: a, Mode: manifest.ModeActive, Name: "alpha-dup"})
	if _, err := c.CreatePackage(dup); err == nil {
		t.Error("two active members from one repo must be rejected when the package is defined")
	}
}

func TestCreateSessionActiveAndReference(t *testing.T) {
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
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.CreateSession(p, "token-refresh", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"alpha/README.md", "beta/README.md", "_ref/contracts/README.md", "AGENTS.md", "CLAUDE.md", ".gitignore", ".vmono/session.toml", "alpha/.env.local"} {
		if _, err := os.Stat(filepath.Join(s.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if s.S.Branch != "vmono/demo/token-refresh" {
		t.Errorf("default session branch = %q", s.S.Branch)
	}
	if br := gitx.CurrentBranch(filepath.Join(s.Dir, "alpha")); br != "vmono/demo/token-refresh" {
		t.Errorf("alpha branch = %q", br)
	}
	if st, _ := gitx.StatusOf(filepath.Join(s.Dir, "_ref", "contracts")); !st.Detached {
		t.Error("reference member must be detached")
	}
	agents, _ := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
	for _, want := range []string{"test purpose", "role of alpha", "session token-refresh", "vmono/demo/token-refresh"} {
		if !strings.Contains(string(agents), want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, agents)
		}
	}

	// The session has its own repo with an initial commit, and ignores members.
	if !gitx.HasCommits(s.Dir) {
		t.Error("session repo needs an initial commit")
	}
	if out := testutil.Git(t, s.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("session repo should be clean after create, got:\n%s", out)
	}
	if log := testutil.Git(t, s.Dir, "log", "--format=%s"); log != "vmono: create session token-refresh" {
		t.Errorf("session commit subject = %q", log)
	}
	// The package repo does not see the session at all.
	if out := testutil.Git(t, p.Dir, "status", "--porcelain"); out != "" {
		t.Errorf("package repo must ignore sessions, got:\n%s", out)
	}
	// The source repo's checked-out branch is untouched.
	if br := gitx.CurrentBranch(a); br != "main" {
		t.Errorf("source branch changed to %q", br)
	}

	if _, err := c.Load("demo"); err != nil {
		t.Errorf("Load: %v", err)
	}
	if got, err := c.LoadSession(p, "token-refresh"); err != nil || got.S.Branch != s.S.Branch {
		t.Errorf("LoadSession = %+v, %v", got, err)
	}
	if sums, _ := c.List(true); len(sums) != 1 || sums[0].Members != 3 || sums[0].Sessions != 1 || sums[0].Dirty {
		t.Errorf("List = %+v", sums)
	}
	if _, err := c.CreateSession(p, "token-refresh", SessionOptions{}); err == nil {
		t.Error("creating an existing session must fail")
	}
}

func TestTwoSessionsSideBySide(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	p, err := c.CreatePackage(request("duo", []string{a, b}, nil))
	if err != nil {
		t.Fatal(err)
	}
	one, err := c.CreateSession(p, "one", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	two, err := c.CreateSession(p, "two", SessionOptions{Branch: "feat/PLAT-412"})
	if err != nil {
		t.Fatal(err)
	}
	if got := gitx.CurrentBranch(filepath.Join(one.Dir, "alpha")); got != "vmono/duo/one" {
		t.Errorf("session one branch = %q", got)
	}
	if got := gitx.CurrentBranch(filepath.Join(two.Dir, "alpha")); got != "feat/PLAT-412" {
		t.Errorf("session two branch = %q", got)
	}
	if list, _ := gitx.WorktreeList(a); len(list) != 3 {
		t.Errorf("source should list its own tree plus one worktree per session: %+v", list)
	}
	names, _ := p.Sessions()
	if strings.Join(names, ",") != "one,two" {
		t.Errorf("Sessions = %v", names)
	}

	// Work in one session is invisible to the other.
	testutil.WriteFile(t, filepath.Join(one.Dir, "alpha", "scratch.txt"), "x")
	if st, _ := gitx.StatusOf(filepath.Join(two.Dir, "alpha")); st.Untracked != 0 {
		t.Error("sessions must have separate worktrees")
	}
	rows, err := c.ListSessions(p, true)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListSessions = %+v, %v", rows, err)
	}
	dirty := map[string]bool{}
	for _, r := range rows {
		dirty[r.Name] = r.Dirty
	}
	if !dirty["one"] || dirty["two"] {
		t.Errorf("dirty flags = %v", dirty)
	}
	if sums, _ := c.List(true); len(sums) != 1 || sums[0].Sessions != 2 || !sums[0].Dirty {
		t.Errorf("package should be dirty when any session is: %+v", sums)
	}
}

func TestCreateSessionRollbackOnFailure(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")

	// A copy pattern that escapes the repo fails after both worktrees exist.
	m := request("rb", []string{a, b}, nil)
	m.Members[1].Copy = []string{"../escape"}
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan apply.Event, 256)
	c.Events = ch
	_, err = c.CreateSession(p, "try", SessionOptions{})
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

	if _, err := os.Stat(p.SessionDir("try")); !os.IsNotExist(err) {
		t.Errorf("session dir must be removed on rollback: %v", err)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Error("a failed session must leave its package alone")
	}
	for _, src := range []string{a, b} {
		if gitx.BranchExists(src, "vmono/rb/try") {
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
	testutil.Git(t, a, "branch", "vmono/again/s1")

	p, err := c.CreatePackage(request("again", []string{a}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateSession(p, "s1", SessionOptions{})
	var be *BranchExistsError
	if !errors.As(err, &be) {
		t.Fatalf("want BranchExistsError, got %v", err)
	}
	s, err := c.CreateSession(p, "s1", SessionOptions{ReuseBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if br := gitx.CurrentBranch(filepath.Join(s.Dir, "alpha")); br != "vmono/again/s1" {
		t.Errorf("branch = %q", br)
	}
}

func TestFailedCreateKeepsPreExistingBranch(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	testutil.Git(t, a, "branch", "vmono/keep/s")
	m := request("keep", []string{a, b}, nil)
	m.Members[1].Copy = []string{"../escape"}
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSession(p, "s", SessionOptions{ReuseBranch: true}); err == nil {
		t.Fatal("expected the copy failure")
	}
	if !gitx.BranchExists(a, "vmono/keep/s") {
		t.Error("a pre-existing branch must survive a failed create")
	}
	if gitx.BranchExists(b, "vmono/keep/s") {
		t.Error("a branch created by the failed run must be removed")
	}
}

func TestSessionBranchRefConflictIsExplained(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	// A migrated package's session keeps the legacy branch vmono/<pkg>, which
	// stops git from also holding vmono/<pkg>/<session>.
	testutil.Git(t, a, "branch", "vmono/clash")
	p, err := c.CreatePackage(request("clash", []string{a}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateSession(p, "next", SessionOptions{})
	if err == nil || !strings.Contains(err.Error(), "conflicts with existing branch \"vmono/clash\"") || !strings.Contains(err.Error(), "different branch name") {
		t.Fatalf("want a conflict explanation, got %v", err)
	}
	if _, err := c.CreateSession(p, "next", SessionOptions{Branch: "feat/next"}); err != nil {
		t.Errorf("an explicit branch should work around it: %v", err)
	}
}

func TestResolveSession(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, err := c.CreatePackage(request("res", []string{a}, nil))
	if err != nil {
		t.Fatal(err)
	}

	var sce *SessionChoiceError
	if _, err := c.ResolveSession("res"); !errors.As(err, &sce) || !errors.Is(err, ErrNotFound) || len(sce.Sessions) != 0 {
		t.Fatalf("a package with no sessions: %v", err)
	}
	one, err := c.CreateSession(p, "one", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := c.ResolveSession("res"); err != nil || s.Name != "one" {
		t.Errorf("a bare package names its only session: %+v, %v", s, err)
	}
	if _, err := c.CreateSession(p, "two", SessionOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveSession("res"); !errors.As(err, &sce) || strings.Join(sce.Sessions, ",") != "one,two" || !strings.Contains(err.Error(), "res/NAME") {
		t.Errorf("ambiguous package should list its sessions: %v", err)
	}
	if s, err := c.ResolveSession("res/two"); err != nil || s.Name != "two" || s.Label() != "res/two" {
		t.Errorf("explicit session: %+v, %v", s, err)
	}
	if _, err := c.ResolveSession("res/ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session = %v", err)
	}

	// From inside a member of a session, with no argument.
	t.Chdir(filepath.Join(one.Dir, "alpha"))
	if s, err := c.ResolveSession(""); err != nil || s.Name != "one" || s.Pkg.Name != "res" {
		t.Errorf("from a member dir: %+v, %v", s, err)
	}
	if pk, err := c.ResolvePackage(""); err != nil || pk.Name != "res" {
		t.Errorf("package from a member dir: %+v, %v", pk, err)
	}
	// From the package directory, outside any session, it is ambiguous.
	t.Chdir(p.Dir)
	if _, err := c.ResolveSession(""); !errors.As(err, &sce) {
		t.Errorf("from the package dir with two sessions: %v", err)
	}
	if pk, err := c.ResolvePackage("res/one"); err != nil || pk.Name != "res" {
		t.Errorf("ResolvePackage should accept pkg/session: %+v, %v", pk, err)
	}
	// VMONO_ROOT is a session dir inside a harness.
	t.Chdir(t.TempDir())
	t.Setenv("VMONO_ROOT", one.Dir)
	if s, err := c.ResolveSession(""); err != nil || s.Name != "one" {
		t.Errorf("via VMONO_ROOT: %+v, %v", s, err)
	}
}

func TestDeleteSessionKeepsPackageAndSiblings(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, err := c.CreatePackage(request("sib", []string{a}, nil))
	if err != nil {
		t.Fatal(err)
	}
	one, _ := c.CreateSession(p, "one", SessionOptions{})
	two, _ := c.CreateSession(p, "two", SessionOptions{})

	testutil.WriteFile(t, filepath.Join(one.Dir, "alpha", "scratch.txt"), "x")
	_, err = c.DeleteSession(one, DeleteOptions{})
	var re *RefusedError
	if !errors.As(err, &re) || len(re.Unsafe) != 1 || re.Unsafe[0].Label() != "one/alpha" {
		t.Fatalf("want refusal naming one/alpha, got %v", err)
	}
	if _, err := os.Stat(one.Dir); err != nil {
		t.Fatal("refused delete must leave the session")
	}
	if _, err := c.DeleteSession(one, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(one.Dir); !os.IsNotExist(err) {
		t.Error("session dir should be gone")
	}
	if _, err := os.Stat(filepath.Join(two.Dir, "alpha", "README.md")); err != nil {
		t.Error("the sibling session must be untouched")
	}
	if _, err := c.Load("sib"); err != nil {
		t.Errorf("the package must survive: %v", err)
	}
	if _, err := c.LoadSession(p, "one"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LoadSession after delete = %v", err)
	}
	if list, _ := gitx.WorktreeList(a); len(list) != 2 {
		t.Errorf("source should list its tree and the sibling's worktree: %+v", list)
	}
}

func TestDeleteRefusesLostWorkThenForce(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	p, s := newWork(t, c, request("del", []string{a, b}, nil))

	// alpha: untracked file. beta: unpushed commit.
	testutil.WriteFile(t, filepath.Join(s.Dir, "alpha", "scratch.txt"), "x")
	testutil.WriteFile(t, filepath.Join(s.Dir, "beta", "feature.txt"), "x")
	testutil.Git(t, filepath.Join(s.Dir, "beta"), "add", "-A")
	testutil.Git(t, filepath.Join(s.Dir, "beta"), "commit", "--quiet", "-m", "work")

	_, err := c.Delete(p, DeleteOptions{})
	var re *RefusedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &re) || len(re.Unsafe) != 2 {
		t.Fatalf("want RefusedError for both members, got %v", err)
	}
	if !strings.Contains(err.Error(), "work/alpha") || !strings.Contains(err.Error(), "work/beta") {
		t.Errorf("rows should name session/member: %v", err)
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

func TestDeletePackageRefusedByAnySession(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, err := c.CreatePackage(request("any", []string{a}, nil))
	if err != nil {
		t.Fatal(err)
	}
	clean, _ := c.CreateSession(p, "clean", SessionOptions{})
	messy, _ := c.CreateSession(p, "messy", SessionOptions{})
	testutil.WriteFile(t, filepath.Join(messy.Dir, "alpha", "wip.txt"), "x")

	rows, err := c.PackageSafety(p)
	if err != nil || len(unsafeOf(rows)) != 1 || unsafeOf(rows)[0].Session != "messy" {
		t.Fatalf("PackageSafety = %+v, %v", rows, err)
	}
	if _, err := c.Delete(p, DeleteOptions{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("want refusal from the messy session, got %v", err)
	}
	for _, s := range []*Session{clean, messy} {
		if _, err := os.Stat(filepath.Join(s.Dir, "alpha")); err != nil {
			t.Errorf("a refused package delete must remove nothing: %v", err)
		}
	}
	if _, err := c.Delete(p, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Error("package should be gone")
	}
}

func TestDeleteCleanKeepsBranchesByDefault(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("clean", []string{a}, nil))
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 0 {
		t.Fatalf("fresh session should be safe: %+v", bad)
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
	_, s := newWork(t, c, request("st", []string{a}, nil))
	testutil.WriteFile(t, filepath.Join(s.Dir, "alpha", "README.md"), "changed")
	st := c.Status(s)
	if len(st.Members) != 1 || st.Members[0].Dirty != 1 || st.Members[0].Branch != "vmono/st" || st.Session != "work" || st.Package != "st" {
		t.Errorf("status = %+v", st)
	}
	if err := os.RemoveAll(filepath.Join(s.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(s); !st.Members[0].Missing {
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
