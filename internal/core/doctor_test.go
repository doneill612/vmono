package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

// kinds returns the finding kinds in a result, in order.
func kinds(r DoctorResult) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func TestDoctorHealthyPackageHasNoFindings(t *testing.T) {
	c, env := newCore(t)
	newPackage(t, c, env, "fine")
	res, err := c.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Packages != 1 || res.Remaining() != 0 {
		t.Errorf("healthy result = %+v", res)
	}
}

func TestDoctorFindsAndFixesMissingWorktreeAndStaleEntry(t *testing.T) {
	c, env := newCore(t)
	p, _ := newPackage(t, c, env, "drift")
	if err := os.RemoveAll(filepath.Join(p.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}

	res, _ := c.Doctor(false)
	got := strings.Join(kinds(res), ",")
	if !strings.Contains(got, KindMissingWorktree) || !strings.Contains(got, KindStaleWorktree) || res.Remaining() != 2 {
		t.Fatalf("report-only findings = %v", res.Findings)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "alpha")); !os.IsNotExist(err) {
		t.Fatal("doctor without --fix must not change anything")
	}

	res, err := c.Doctor(true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remaining() != 0 {
		t.Errorf("fix left findings: %+v", res.Findings)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "alpha", "README.md")); err != nil {
		t.Error("worktree not recreated")
	}
	if again, _ := c.Doctor(false); again.Remaining() != 0 {
		t.Errorf("not clean after fix: %+v", again.Findings)
	}
}

func TestDoctorRelocatesMovedSourceByRemote(t *testing.T) {
	c, env := newCore(t)
	p, a := newPackage(t, c, env, "moved")
	c.Cfg.SourceRoots = []string{filepath.Dir(a)}

	// Move the source repo elsewhere under the source root; it keeps its
	// remote. Detach the package's worktree registration first by removing
	// the worktree directory so the move is clean.
	if err := os.RemoveAll(filepath.Join(p.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(a), "alpha-renamed")
	if err := os.Rename(a, moved); err != nil {
		t.Fatal(err)
	}

	res, _ := c.Doctor(false)
	if !strings.Contains(strings.Join(kinds(res), ","), KindMovedSource) || res.Remaining() == 0 {
		t.Fatalf("report-only = %+v", res.Findings)
	}
	res, err := c.Doctor(true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Remaining() != 0 {
		t.Fatalf("fix left findings: %+v", res.Findings)
	}
	reloaded, _ := c.Load("moved")
	if !strings.HasSuffix(reloaded.M.Members[0].Source, "alpha-renamed") {
		t.Errorf("source not relocated: %q", reloaded.M.Members[0].Source)
	}
	if br := gitx.CurrentBranch(filepath.Join(p.Dir, "alpha")); br != "vmono/moved" {
		t.Errorf("worktree not rebuilt from the new source: %q", br)
	}
}

func TestDoctorReportsUnfixableAndRebuildsIndex(t *testing.T) {
	c, env := newCore(t)
	p, a := newPackage(t, c, env, "gone")
	root, _ := c.PackageRoot()

	if err := os.MkdirAll(filepath.Join(root, "random-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(p.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	// A package that exists on disk but the index never heard of, and an
	// index entry for one that vanished.
	c.Idx.RemovePackage("gone")
	c.Idx.SetPackage("ghost", filepath.Join(root, "ghost"))

	res, err := c.Doctor(true)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(kinds(res), ",")
	for _, want := range []string{KindOrphanDir, KindMissingSource, KindIndexRebuilt} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if res.Remaining() != 2 {
		t.Errorf("orphan dir and missing source should remain: %+v", res.Findings)
	}
	if _, ok := c.Idx.Packages["gone"]; !ok {
		t.Error("index should have been rebuilt to include the package on disk")
	}
	if _, ok := c.Idx.Packages["ghost"]; ok {
		t.Error("vanished package should be dropped from the index")
	}
	if _, err := os.Stat(filepath.Join(root, "random-dir")); err != nil {
		t.Error("doctor must never delete orphan directories")
	}
}

func TestDoctorBadManifest(t *testing.T) {
	c, env := newCore(t)
	_ = env
	root, _ := c.PackageRoot()
	bad := filepath.Join(root, "broken")
	testutil.WriteFile(t, filepath.Join(bad, ".vmono", "manifest.toml"), "not = [valid")
	res, err := c.Doctor(true)
	if err != nil || len(res.Findings) == 0 || res.Findings[0].Kind != KindBadManifest || res.Remaining() != 1 {
		t.Errorf("bad manifest result = %+v, %v", res, err)
	}
}

func TestInspect(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	ref := env.NewSource(t, "contracts")
	p, err := c.Create(request("insp", []string{a}, []string{ref}), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r)
	if r.Name != "insp" || len(r.Members) != 2 || r.Harness == nil || r.Harness.Name != "claude" {
		t.Fatalf("inspect = %s", data)
	}
	m := r.Members[0]
	want, _ := filepath.EvalSymlinks(filepath.Join(a, ".git"))
	got, _ := filepath.EvalSymlinks(m.GitCommonDir)
	if !m.Exists || got != want || m.Branch != "vmono/insp" || !filepath.IsAbs(m.Path) || m.RelPath != "alpha" {
		t.Errorf("active member = %+v", m)
	}
	if r.Members[1].Ref != "origin/HEAD" || r.Members[1].RelPath != "_ref/contracts" {
		t.Errorf("reference member = %+v", r.Members[1])
	}
	if r.LaunchEnv["VMONO_AUTHOR"] != "agent:claude" || r.LaunchEnv["VMONO_ROOT"] != p.Dir {
		t.Errorf("launch env = %v", r.LaunchEnv)
	}
}
