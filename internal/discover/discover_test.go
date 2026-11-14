package discover

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/index"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestScanDepthAndSkip(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	root := filepath.Dir(a)

	nested := filepath.Join(root, "group", "gamma")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, nested, "init", "--quiet")
	tooDeep := filepath.Join(root, "x", "y", "z")
	if err := os.MkdirAll(tooDeep, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, tooDeep, "init", "--quiet")

	got := Scan([]string{root}, 2, nil)
	if len(got) != 2 {
		t.Fatalf("depth 2 should find alpha and gamma, got %v", got)
	}
	if got := Scan([]string{root}, 1, nil); len(got) != 1 {
		t.Fatalf("depth 1 should find only alpha, got %v", got)
	}
	if got := Scan([]string{root}, 2, []string{a}); len(got) != 1 {
		t.Fatalf("skip should exclude alpha, got %v", got)
	}
}

func TestScanSkipsWorktreeCheckouts(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	root := filepath.Dir(src)
	wt := filepath.Join(root, "alpha-wt")
	if err := gitx.WorktreeAddBranch(src, wt, "feature", "origin/HEAD"); err != nil {
		t.Fatal(err)
	}
	got := Scan([]string{root}, 2, nil)
	if len(got) != 1 || !samePath(got[0], src) {
		t.Fatalf("worktree checkout must not be discovered as a repo: %v", got)
	}
}

func TestReposMergeAndResolve(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	root := filepath.Dir(a)

	idx := index.LoadFrom(filepath.Join(env.Root, "idx.json"))
	idx.AddRepo(index.Repo{Path: b})
	idx.AddRepo(index.Repo{Path: filepath.Join(env.Root, "gone")})

	repos := Repos([]string{root}, []string{a}, idx, 2, nil)
	if len(repos) != 2 {
		t.Fatalf("expected 2 de-duplicated repos, got %+v", repos)
	}
	if repos[0].Name != "alpha" || repos[0].Branch != "main" || repos[0].Remote == "" {
		t.Errorf("repo metadata wrong: %+v", repos[0])
	}

	r, err := Resolve("beta", repos)
	if err != nil || !samePath(r.Path, b) {
		t.Fatalf("resolve by name: %+v, %v", r, err)
	}
	sub := filepath.Join(a, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err = Resolve(sub, repos)
	if err != nil || !samePath(r.Path, a) {
		t.Fatalf("resolve by path should find repo root: %+v, %v", r, err)
	}
	var nf *NotFoundError
	if _, err := Resolve(filepath.Join(env.Root, "nothing"), repos); !errors.As(err, &nf) {
		t.Errorf("expected NotFoundError, got %v", err)
	}
	dup := append(repos, Repo{Name: "beta", Path: "/elsewhere/beta"})
	var amb *AmbiguousError
	if _, err := Resolve("beta", dup); !errors.As(err, &amb) {
		t.Errorf("expected AmbiguousError, got %v", err)
	}
}

// samePath compares paths after resolving symlinks, since git reports the
// real path (/private/var on macOS) for a temp directory under /var.
func samePath(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestIndexRoundTripAndOrdering(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state", "index.json")
	idx := index.LoadFrom(file)
	idx.SetPackage("one", "/p/one")
	idx.SetPackage("two", "/p/two")
	idx.RecordLaunch("two", "claude", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	idx.AddRepo(index.Repo{Path: "/r/x"})
	if idx.AddRepo(index.Repo{Path: "/r/x"}) {
		t.Error("duplicate AddRepo should report false")
	}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
	got := index.LoadFrom(file)
	sorted := got.SortedPackages()
	if len(sorted) != 2 || sorted[0].Name != "two" || len(got.Launches) != 1 || len(got.Repos) != 1 {
		t.Errorf("round trip/order wrong: %+v", got)
	}
	if bad := index.LoadFrom(filepath.Join(t.TempDir(), "missing.json")); len(bad.Packages) != 0 {
		t.Error("missing file should yield empty index")
	}
}
