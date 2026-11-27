package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// useSourceRoot writes a config scanning the fixtures' source directory,
// with a harness that exists on every unix CI image.
func useSourceRoot(t *testing.T, env *testutil.Env) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), `
source_roots = ["`+filepath.ToSlash(filepath.Join(env.Root, "source"))+`"]
default_harness = "fake"

[harness.fake]
command = "sh"
`)
}

// loadPkg loads a package through a fresh Core.
func loadPkg(t *testing.T, name string) *core.Package {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Load(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateFlowEditorPickerProgressLaunch(t *testing.T) {
	env := testutil.NewEnv(t)
	useSourceRoot(t, env)
	env.NewSource(t, "alpha")
	env.NewSource(t, "beta")
	env.NewSource(t, "contracts")
	d := newDriver(t, env, "")

	d.key("n")
	d.has("New package", "Name", "Purpose", "Context", "Harness", "Members")
	d.typeText("Bad Name")
	d.key("ctrl+s")
	d.has("invalid name")
	for range "Bad Name" {
		d.key("backspace")
	}
	d.typeText("shiny")
	d.key("tab")
	d.typeText("Ship the shiny thing.")
	d.key("tab", "tab") // context, harness
	d.key("tab")        // members
	d.key("ctrl+s")     // no members yet -> opens the picker
	d.has("Choose repos", "alpha", "beta", "contracts")

	d.key("space")             // alpha: active
	d.key("down", "down", "r") // contracts: reference
	d.key("enter")
	d.has("New package", "alpha", "contracts")
	d.key("r") // role for the first member
	d.typeText("the gateway")
	d.key("enter")
	d.has("the gateway")

	d.key("ctrl+s")
	d.eventually(func() bool { return containsView(d, "shiny") && !containsView(d, "New package") }, "packages screen")
	d.lacks("new session")
	d.key("right") // open the package's (empty) sessions view
	d.key("n")
	d.eventually(func() bool { return containsView(d, "new session") }, "new session prompt")
	d.has("package shiny", "<session>")
	d.typeText("task")
	d.has("session:  task")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.has("default", "shiny/task")

	p := loadPkg(t, "shiny")
	if p.M.Purpose != "Ship the shiny thing." || len(p.M.Members) != 2 {
		t.Fatalf("manifest = %+v", p.M)
	}
	if p.M.Members[0].Name != "alpha" || p.M.Members[0].Role != "the gateway" || p.M.Members[0].Mode != manifest.ModeActive {
		t.Errorf("alpha = %+v", p.M.Members[0])
	}
	if p.M.Members[1].Name != "contracts" || p.M.Members[1].Mode != manifest.ModeReference {
		t.Errorf("contracts = %+v", p.M.Members[1])
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "task", "_ref", "contracts", "README.md")); err != nil {
		t.Error("reference worktree missing from the session")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "alpha")); err == nil {
		t.Error("the package directory must not hold worktrees")
	}
	d.key("enter")
	if d.app.Launch() == nil || d.app.Launch().Package != "shiny" || d.app.Launch().Session != "task" || d.app.Launch().Harness != "fake" {
		t.Fatalf("launch = %+v", d.app.Launch())
	}
}

func TestCloneFlowStartsFromTheSource(t *testing.T) {
	env := testutil.NewEnv(t)
	useSourceRoot(t, env)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "contracts")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePackage(&manifest.Manifest{
		Name: "docs", Purpose: "Explain it.", Context: "ticket 7", Harness: "fake",
		Members: []manifest.Member{
			{Source: a, Mode: manifest.ModeReference, Role: "the gateway"},
			{Source: b, Mode: manifest.ModeReference},
		},
		Notes: []manifest.Note{{At: time.Date(2026, 11, 14, 10, 5, 0, 0, time.UTC), By: "user", Text: "frozen"}},
		Init: &manifest.Init{
			At: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), By: "agent:claude",
			Members: []string{"alpha", "contracts"}, Overview: "how they fit",
		},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, env, "")

	d.key("C")
	d.has("Clone · docs", "docs-copy", "Explain it.", "ticket 7", "fake", "alpha", "the gateway", "contracts")
	for range "-copy" {
		d.key("backspace")
	}
	d.typeText("-work")
	d.key("tab", "tab", "tab", "tab") // members
	d.key("m")                        // alpha becomes active
	d.key("ctrl+s")
	d.eventually(func() bool { return containsView(d, "docs-work") && !containsView(d, "Clone · docs") }, "packages screen")
	d.has("i re-initializes it")

	p := loadPkg(t, "docs-work")
	alpha, _ := p.M.Member("alpha")
	if alpha.Mode != manifest.ModeActive || alpha.Path != "alpha" || alpha.Role != "the gateway" {
		t.Errorf("alpha = %+v", alpha)
	}
	if other, _ := p.M.Member("contracts"); other.Mode != manifest.ModeReference {
		t.Errorf("contracts = %+v", other)
	}
	if p.M.Harness != "fake" || len(p.M.Notes) != 1 || p.M.Overview() != "how they fit" || p.M.DriftText() != "" {
		t.Errorf("clone lost something: %+v", p.M)
	}
	if src := loadPkg(t, "docs"); src.M.Members[0].Mode != manifest.ModeReference {
		t.Errorf("the source changed: %+v", src.M.Members[0])
	}
}

func TestTogglingAModeBackRestoresTheMember(t *testing.T) {
	env := testutil.NewEnv(t)
	useSourceRoot(t, env)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "tag", "v3")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreatePackage(&manifest.Manifest{
		Name: "pinned", Members: []manifest.Member{{Source: a, Mode: manifest.ModeReference, Ref: "v3"}},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, env, "")

	d.key("C")
	d.key("tab", "tab", "tab", "tab") // members
	d.key("m")
	d.has("v3 is not a branch")
	d.key("m", "ctrl+s")
	d.eventually(func() bool { return containsView(d, "pinned-copy") && !containsView(d, "Clone · pinned") }, "packages screen")

	if mem := loadPkg(t, "pinned-copy").M.Members[0]; mem.Mode != manifest.ModeReference || mem.Ref != "v3" {
		t.Errorf("alpha = %+v", mem)
	}
}

func TestCreateAsksBeforeReusingExistingBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	useSourceRoot(t, env)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "branch", "work")
	d := newDriver(t, env, "")

	d.key("n")
	d.typeText("reuse")
	d.key("ctrl+s")
	d.has("Choose repos")
	d.key("space", "enter", "ctrl+s")
	d.eventually(func() bool { return containsView(d, "reuse") && !containsView(d, "New package") }, "packages screen")
	d.lacks("new session")
	d.key("right", "n")
	d.eventually(func() bool { return containsView(d, "new session") }, "new session prompt")
	d.typeText("work")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "already exists") }, "reuse prompt")
	d.has("Check it out instead")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt after reuse")
	if br := testutil.Git(t, filepath.Join(env.PackageRoot, "reuse", "work", "alpha"), "branch", "--show-current"); br != "work" {
		t.Errorf("branch = %q", br)
	}
}

func TestEditFlowRoleAndRemovalWithConfirm(t *testing.T) {
	env := testutil.NewEnv(t)
	useSourceRoot(t, env)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	seed(t, env, "work", a, b)
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "work", "work", "beta", "wip.txt"), "x")
	d := newDriver(t, env, "")

	d.key("e")
	d.has("Edit · work", "work", "read-only")
	d.key("tab", "tab", "tab") // purpose -> context -> harness -> members
	d.key("r")
	for range "role" {
		d.key("backspace")
	}
	d.typeText("changed role")
	d.key("enter")
	d.key("down", "x") // remove beta, which has untracked work
	d.lacks("beta")
	d.key("ctrl+s")
	d.has("Discard unsaved work?", "work/beta", "untracked", "Type the package name")
	d.typeText("work")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "enter back to packages") }, "edit to finish")

	p := loadPkg(t, "work")
	if len(p.M.Members) != 1 || p.M.Members[0].Role != "changed role" {
		t.Fatalf("manifest after edit = %+v", p.M.Members)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "work", "beta")); !os.IsNotExist(err) {
		t.Error("beta worktree should be gone from the session")
	}
}

func TestEditNoChangesReturnsHome(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "same", a)
	d := newDriver(t, env, "")
	d.key("e", "ctrl+s")
	d.has("packages", "no changes")
}

func TestEditCancelWithEscAndHarnessCycle(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "cyc", a)
	d := newDriver(t, env, "")
	d.key("e", "tab", "tab") // harness field
	d.has("(default: claude)")
	d.key("right")
	d.has("claude ▸")
	d.key("ctrl+s")
	d.eventually(func() bool { return containsView(d, "enter back to packages") }, "edit to finish")
	if p := loadPkg(t, "cyc"); p.M.Harness != "claude" {
		t.Errorf("harness = %q", p.M.Harness)
	}
	d.key("esc")
	d.has("packages")
	d.key("e", "esc")
	d.has("packages")
}

func TestPickerAddByPathWithCompletion(t *testing.T) {
	env := testutil.NewEnv(t)
	// No source root configured: the repo is only reachable by path.
	a := env.NewSource(t, "elsewhere")
	d := newDriver(t, env, "")

	d.key("n")
	d.typeText("bypath")
	d.key("ctrl+s")
	d.has("Choose repos", "No repos found")
	d.key("p")
	d.has("find repo")
	d.key("ctrl+u") // the finder starts at ~/; type a path from scratch
	parent := filepath.Dir(a)
	d.typeText(parent + string(os.PathSeparator) + "else")
	d.has("elsewhere")
	d.key("tab") // completes the highlighted repo
	d.key("enter")
	d.has("added elsewhere", "[x]")
	d.key("enter", "ctrl+s")
	d.eventually(func() bool { return containsView(d, "bypath") && !containsView(d, "New package") }, "packages screen")
	d.lacks("new session")
	if p := loadPkg(t, "bypath"); len(p.M.Members) != 1 || p.M.Members[0].Name != "elsewhere" {
		t.Errorf("members = %+v", p.M.Members)
	}
	// The path was remembered for next time.
	d.key("esc")
	d.key("n")
	d.typeText("again")
	d.key("ctrl+s")
	d.eventually(func() bool { return containsView(d, "elsewhere") }, "remembered repo")
}

func TestPickerRejectsNonRepoPath(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")
	d.key("n")
	d.typeText("x")
	d.key("ctrl+s", "p")
	d.key("ctrl+u")
	d.typeText(filepath.Join(env.Root, "does-not-exist"))
	d.key("enter")
	d.has("repo not found")
}

func TestExternalEditorResultLoadsIntoField(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")
	d.key("n", "tab") // purpose
	f := filepath.Join(env.Root, "purpose.md")
	testutil.WriteFile(t, f, "Written in $EDITOR.\nSecond line.\n")
	d.send(editedTextMsg{field: fieldPurpose, file: f})
	d.has("Written in $EDITOR.", "Second line.")
	if _, err := os.Stat(f); !os.IsNotExist(err) {
		t.Error("temp file should be removed after reading")
	}
}

func TestCompletePath(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"alpha-one", "alpha-two", "beta", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(t, filepath.Join(root, "alpha-file"), "x")
	sep := string(os.PathSeparator)

	got, cands := completePath(root + sep + "al")
	if got != root+sep+"alpha-" || len(cands) != 2 {
		t.Errorf("common prefix = %q, %v", got, cands)
	}
	if got, cands = completePath(root + sep + "be"); got != root+sep+"beta"+sep || len(cands) != 1 {
		t.Errorf("single match = %q, %v", got, cands)
	}
	if _, cands = completePath(root + sep); len(cands) != 3 || strings.Contains(strings.Join(cands, ","), ".hidden") {
		t.Errorf("hidden dirs must be skipped unless asked: %v", cands)
	}
	if _, cands = completePath(root + sep + ".h"); len(cands) != 1 {
		t.Errorf("explicit dot prefix should list hidden: %v", cands)
	}
	if got, cands = completePath(root + sep + "zzz"); got != root+sep+"zzz" || cands != nil {
		t.Errorf("no match = %q, %v", got, cands)
	}
	if got, _ = completePath(""); got != "" {
		t.Errorf("empty input = %q", got)
	}
}

func TestEditMemberCommands(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "cmds", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("e", "tab", "tab", "tab", "c")
	d.has("setup and teardown", "member alpha")
	d.typeText("pnpm install")
	d.key("enter")
	d.typeText("make")
	d.key("tab")
	d.typeText("rm -rf out")
	d.key("ctrl+s")
	d.has("Edit · cmds", "setup, teardown")
	d.key("c")
	d.has("pnpm install", "rm -rf out")
	d.typeText("oops")
	d.key("esc", "ctrl+s") // a cancelled dialog changes nothing
	d.eventually(func() bool { return containsView(d, "enter back to packages") }, "edit to finish")

	mem := loadPkg(t, "cmds").M.Members[0]
	if strings.Join(mem.Setup, "|") != "pnpm install|make" || strings.Join(mem.Teardown, "|") != "rm -rf out" {
		t.Errorf("member = %+v", mem)
	}
	d.key("enter", "e", "tab", "tab", "tab", "c")
	d.has("pnpm install", "make") // and it can be taken out again
	for range "pnpm install\nmake" {
		d.key("backspace")
	}
	d.key("ctrl+s", "ctrl+s")
	d.eventually(func() bool { return containsView(d, "enter back to packages") }, "edit to finish")
	if mem := loadPkg(t, "cmds").M.Members[0]; len(mem.Setup) != 0 || len(mem.Teardown) != 1 {
		t.Errorf("after clearing = %+v", mem)
	}
}
