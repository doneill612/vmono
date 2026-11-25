package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// openInit opens an init workspace and fails the test if it cannot.
func openInit(t *testing.T, c *Core, p *Package) *InitWorkspace {
	t.Helper()
	w, err := c.OpenInit(p)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// abandon makes a workspace look like its vmono process died.
func abandon(t *testing.T, w *InitWorkspace) {
	t.Helper()
	cmd := exec.Command("git", "--version")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	w.PID = cmd.Process.Pid
	if err := w.save(); err != nil {
		t.Fatal(err)
	}
}

// worktrees counts a repo's worktrees, its own included.
func worktrees(t *testing.T, src string) int {
	t.Helper()
	return strings.Count(testutil.Git(t, src, "worktree", "list", "--porcelain"), "worktree ")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInitWorkspaceWithoutSessions(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	m := request("demo", []string{a}, []string{b})
	m.Members[0].Setup = manifest.Commands{"echo ran > setup-ran"}
	m.Members[0].Path = "code/alpha"
	m.Members[1].Path = "_ref/elsewhere"
	p := mustPackage(t, c, m)
	before := manifest.Encode(p.M)
	branches := testutil.Git(t, a, "branch", "-a")

	w := openInit(t, c, p)
	for _, rel := range []string{"_ref/alpha/README.md", "_ref/beta/README.md", "AGENTS.md", ".git"} {
		if !exists(filepath.Join(w.Dir, filepath.FromSlash(rel))) {
			t.Errorf("missing %s", rel)
		}
	}
	if exists(filepath.Join(w.Dir, "_ref", "alpha", "setup-ran")) {
		t.Error("setup must not run")
	}
	if names, _ := p.Sessions(); len(names) != 0 {
		t.Errorf("the workspace is not a session: %v", names)
	}
	if got := testutil.Git(t, p.Dir, "status", "--porcelain"); strings.TrimSpace(got) != "" {
		t.Errorf("the package repo must ignore the workspace: %q", got)
	}
	reloaded, _ := c.Load("demo")
	if string(manifest.Encode(reloaded.M)) != string(before) || string(manifest.Encode(p.M)) != string(before) {
		t.Error("the manifest must not change")
	}
	if testutil.Git(t, a, "branch", "-a") != branches {
		t.Error("no branch may be created")
	}

	chdir(t, filepath.Join(w.Dir, "_ref", "alpha"))
	if pp, s, err := c.ResolveContext(""); err != nil || pp.Name != "demo" || s != nil {
		t.Errorf("inside the workspace the package is known and there is no session: %v %v %v", pp, s, err)
	}
	chdir(t, env.Root)

	if err := c.CloseInit(w, false); err != nil {
		t.Fatal(err)
	}
	if exists(w.Dir) || worktrees(t, a) != 1 || worktrees(t, b) != 1 {
		t.Error("closing should leave nothing behind")
	}
}

func TestInitLeavesSessionsAlone(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("demo", []string{a}, nil))
	dirty := filepath.Join(s.Dir, "alpha", "README.md")
	testutil.WriteFile(t, dirty, "work in progress\n")
	head := testutil.Git(t, filepath.Join(s.Dir, "alpha"), "rev-parse", "HEAD")

	w := openInit(t, c, p)
	if got, _ := os.ReadFile(filepath.Join(w.Dir, "_ref", "alpha", "README.md")); string(got) != "# alpha\n" {
		t.Errorf("the workspace should hold the base, not a session's edits: %q", got)
	}
	if err := c.CloseInit(w, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dirty)
	if string(got) != "work in progress\n" || testutil.Git(t, filepath.Join(s.Dir, "alpha"), "rev-parse", "HEAD") != head {
		t.Error("the session was disturbed")
	}
}

func TestInitHandlesARepoTwiceAndNoRemote(t *testing.T) {
	c, env := newCore(t)
	src := filepath.Join(env.Root, "local")
	testutil.WriteFile(t, filepath.Join(src, "f"), "x")
	testutil.Git(t, src, "init", "--quiet")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "--quiet", "-m", "only local")
	m := request("demo", []string{src}, []string{src})
	m.Members[1].Name = "local-ref"
	p := mustPackage(t, c, m)
	w := openInit(t, c, p)
	if err := c.CloseInit(w, false); err != nil {
		t.Errorf("commits on no remote are not changes: %v", err)
	}
}

func TestInitRollsBack(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	p := mustPackage(t, c, request("demo", []string{a, b}, nil))
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenInit(p); err == nil {
		t.Fatal("a missing source should fail")
	}
	if exists(p.InitDir()) || worktrees(t, a) != 1 {
		t.Error("a failed open should leave nothing behind")
	}
}

func TestInitKeepsChanges(t *testing.T) {
	c, env := newCore(t)
	p := mustPackage(t, c, request("demo", []string{env.NewSource(t, "alpha"), env.NewSource(t, "beta")}, nil))
	w := openInit(t, c, p)
	testutil.WriteFile(t, filepath.Join(w.Dir, "_ref", "alpha", "README.md"), "edited")
	testutil.Git(t, filepath.Join(w.Dir, "_ref", "beta"), "commit", "--quiet", "--allow-empty", "-m", "x")

	err := c.CloseInit(w, false)
	var refused *RefusedError
	if !errors.As(err, &refused) || len(refused.Unsafe) != 2 || refused.Unsafe[1].Unpushed != 1 {
		t.Fatalf("close = %v", err)
	}
	if w = c.FindInit(p); w == nil || w.Running() {
		t.Error("a kept workspace has no owner")
	}
	if _, err := c.OpenInit(p); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "--clean") {
		t.Errorf("reopening over changes: %v", err)
	}
	if rows, err := c.PackageSafety(p); err != nil || len(unsafeOf(rows)) != 2 {
		t.Errorf("safety = %v %v", rows, err)
	}
	if _, err := c.Delete(p, DeleteOptions{}); !errors.Is(err, ErrRefused) {
		t.Errorf("delete = %v", err)
	}
	if err := c.CloseInit(w, true); err != nil || exists(w.Dir) {
		t.Errorf("forced close: %v", err)
	}
}

func TestInitLeftovers(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("demo", []string{a}, nil))

	w := openInit(t, c, p)
	w.PID = os.Getppid()
	if err := w.save(); err != nil {
		t.Fatal(err)
	}
	var running *InitRunningError
	if _, err := c.OpenInit(p); !errors.As(err, &running) {
		t.Errorf("another live owner: %v", err)
	}
	if err := c.CloseInit(w, true); !errors.As(err, &running) {
		t.Errorf("force does not take it from a live owner: %v", err)
	}
	if _, err := c.Delete(p, DeleteOptions{Force: true}); !errors.As(err, &running) {
		t.Errorf("delete while running: %v", err)
	}

	abandon(t, w)
	w = openInit(t, c, p) // replaced
	abandon(t, w)
	res, err := c.Doctor(false)
	if err != nil || len(res.Findings) != 1 || res.Findings[0].Kind != KindStaleInit {
		t.Errorf("doctor = %+v %v", res, err)
	}
	if res, _ = c.Doctor(true); !res.Findings[0].Fixed || exists(w.Dir) || worktrees(t, a) != 1 {
		t.Errorf("doctor fix = %+v", res)
	}

	abandon(t, openInit(t, c, p))
	if _, err := c.Delete(p, DeleteOptions{}); err != nil || worktrees(t, a) != 1 {
		t.Errorf("delete should take a leftover along: %v", err)
	}
}

func TestRecordCoversWhatWasCheckedOut(t *testing.T) {
	c, env := newCore(t)
	p := mustPackage(t, c, request("demo", []string{env.NewSource(t, "alpha")}, nil))
	w := openInit(t, c, p)
	if _, err := c.AddMember(p, AddOptions{Source: env.NewSource(t, "beta")}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RecordInit(p, InitRecord{Overview: "o"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.M.Init.Members, []string{"alpha"}) {
		t.Errorf("members = %v", p.M.Init.Members)
	}
	if err := c.CloseInit(w, false); err != nil {
		t.Fatal(err)
	}
}
