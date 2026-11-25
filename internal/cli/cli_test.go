package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/testutil"
)

// run executes the CLI in-process and returns stdout, stderr and the error.
func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRoot(nil)
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errb.String(), err
}

// TestVersionFlag checks that --version prints "vmono (<version>)" with the
// build's version, which is "dev" for a test binary.
func TestVersionFlag(t *testing.T) {
	out, _, err := run(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); !strings.HasPrefix(got, "vmono (") || !strings.HasSuffix(got, ")") || got == "vmono ()" {
		t.Fatalf("--version = %q, want \"vmono (<version>)\"", got)
	}
}

func TestNewLsStatusPathDelete(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	purpose := filepath.Join(env.Root, "purpose.md")
	testutil.WriteFile(t, purpose, "Do the thing.\n")

	out, stderr, err := run(t, "new", "demo", "--active", a, "--ref", b, "--purpose-file", purpose, "--session", "work")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}
	dir := strings.TrimSpace(out)
	pkgDir := filepath.Dir(dir)
	if filepath.Base(dir) != "work" || filepath.Base(pkgDir) != "demo" {
		t.Fatalf("new --session should print the session path, got %q", out)
	}
	if !strings.Contains(stderr, "worktree add") {
		t.Errorf("progress lines missing from stderr: %q", stderr)
	}
	agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if !strings.Contains(string(agents), "Do the thing.") {
		t.Error("purpose not rendered")
	}

	out, _, err = run(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var list []core.Summary
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0].Members != 2 || list[0].Sessions != 1 {
		t.Fatalf("ls --json = %q, %v", out, err)
	}
	if out, _, _ = run(t, "ls"); !strings.Contains(out, "demo") {
		t.Errorf("ls table missing package: %q", out)
	}

	out, _, err = run(t, "status", "demo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st core.SessionStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Members) != 2 {
		t.Fatalf("status --json = %q, %v", out, err)
	}
	if out, _, _ = run(t, "status", "demo"); !strings.Contains(out, "clean") {
		t.Errorf("status table = %q", out)
	}

	if out, _, _ = run(t, "path", "demo"); strings.TrimSpace(out) != pkgDir {
		t.Errorf("path of a package = %q, want %q", out, pkgDir)
	}
	if out, _, _ = run(t, "path", "demo/work"); strings.TrimSpace(out) != dir {
		t.Errorf("path of a session = %q, want %q", out, dir)
	}

	out, _, err = run(t, "repos", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"alpha"`) {
		t.Errorf("path-resolved repos should be remembered: %s", out)
	}

	// Dirty member -> delete refused with exit code 2; force succeeds.
	testutil.WriteFile(t, filepath.Join(dir, "alpha", "scratch"), "x")
	_, _, err = run(t, "delete", "demo")
	if ExitCode(err) != ExitRefused {
		t.Fatalf("delete of dirty package: exit %d, err %v", ExitCode(err), err)
	}
	if _, _, err = run(t, "delete", "demo", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("package should be gone")
	}
}

func TestExitCodes(t *testing.T) {
	env := testutil.NewEnv(t)
	repo := env.NewSource(t, "alpha")
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"path", "ghost"}, ExitNotFound},
		{[]string{"status", "ghost"}, ExitNotFound},
		{[]string{"delete", "ghost"}, ExitNotFound},
		{[]string{"new", "x", "--active", "no-such-repo"}, ExitNotFound},
		{[]string{"new", "x"}, ExitError},
		{[]string{"new", "Bad_Name", "--active", repo}, ExitError},
		{[]string{"session", "rm", "ghost/none"}, ExitNotFound},
		{[]string{"session", "rm", "noslash"}, ExitError},
	}
	for _, tc := range cases {
		_, _, err := run(t, tc.args...)
		if got := ExitCode(err); got != tc.want {
			t.Errorf("%v: exit %d (%v), want %d", tc.args, got, err, tc.want)
		}
	}
}

func TestInitWritesConfig(t *testing.T) {
	env := testutil.NewEnv(t)
	root := filepath.Join(env.Root, "src")
	if _, _, err := run(t, "init", "--source-root", root, "--package-root", filepath.Join(env.Root, "pk")); err != nil {
		t.Fatal(err)
	}
	// Compare decoded values: TOML escapes the backslashes in Windows paths,
	// so the raw file text never contains the path verbatim there.
	cfg, err := config.LoadFrom(filepath.Join(env.Root, "cfg", "config.toml"))
	if err != nil || !cfg.Loaded || len(cfg.SourceRoots) != 1 || cfg.SourceRoots[0] != root {
		t.Fatalf("config not written: %+v, %v", cfg, err)
	}
}

func TestOpenPassesArgsAndHarness(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	if _, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}

	var got *harness.Launch
	var replace bool
	orig := launchHarness
	launchHarness = func(l *harness.Launch, r bool) error { got, replace = l, r; return nil }
	t.Cleanup(func() { launchHarness = orig })

	// "sh" stands in for an installed harness on every unix CI image.
	cfgPath := filepath.Join(env.Root, "cfg", "config.toml")
	testutil.WriteFile(t, cfgPath, "default_harness = \"fake\"\n[harness.fake]\ncommand = \"sh\"\nargs = [\"-c\"]\n")

	if _, _, err := run(t, "open", "demo", "--", "true"); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "fake" || !replace {
		t.Fatalf("launch = %+v replace=%v", got, replace)
	}
	if len(got.Args) != 2 || got.Args[0] != "-c" || got.Args[1] != "true" {
		t.Errorf("args = %v", got.Args)
	}
	if filepath.Base(got.Dir) != "work" || filepath.Base(filepath.Dir(got.Dir)) != "demo" {
		t.Errorf("dir = %q, want the session root", got.Dir)
	}

	_, _, err := run(t, "open", "demo", "--harness", "ghost")
	if ExitCode(err) != ExitNotFound {
		t.Errorf("unknown harness exit = %d (%v)", ExitCode(err), err)
	}
	if _, _, err := run(t, "open", "a", "b"); err == nil {
		t.Error("two packages should be rejected")
	}
}

// TestIntegrationInstallAndUninstallTmux pins the CLI around the tmux
// integration: install wires ~/.tmux.conf and turns on the auto launcher,
// keeps a launcher the user chose, and uninstall puts direct back.
func TestIntegrationInstallAndUninstallTmux(t *testing.T) {
	env := testutil.NewEnv(t)
	conf := filepath.Join(env.Home, ".tmux.conf")

	out, _, err := run(t, "integration", "install", "tmux")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tmux source-file") {
		t.Errorf("install should print the reload hint: %q", out)
	}
	if b, err := os.ReadFile(conf); err != nil || !strings.Contains(string(b), "# vmono integration: tmux") {
		t.Fatalf("~/.tmux.conf = %q, %v", b, err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Launcher != config.LauncherAuto {
		t.Fatalf("launcher after install = %q, %v", cfg.Launcher, err)
	}
	if out, _, _ := run(t, "integration", "ls"); !strings.Contains(out, "installed") {
		t.Errorf("ls = %q", out)
	}
	if out, _, _ := run(t, "integration", "print", "tmux"); !strings.Contains(out, "bind-key V") {
		t.Errorf("print = %q", out)
	}

	if _, _, err := run(t, "integration", "uninstall", "tmux"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(conf); strings.Contains(string(b), "vmono") {
		t.Errorf("~/.tmux.conf after uninstall = %q", b)
	}
	if cfg, _ = config.Load(); cfg.Launcher != config.LauncherDirect {
		t.Errorf("launcher after uninstall = %q", cfg.Launcher)
	}

	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), "launcher = \"tmux\"\n")
	if _, _, err := run(t, "integration", "install", "tmux"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = config.Load(); cfg.Launcher != config.LauncherTmux {
		t.Errorf("install must keep a launcher the user set: %q", cfg.Launcher)
	}

	if _, _, err := run(t, "integration", "install", "screen"); ExitCode(err) != ExitNotFound {
		t.Errorf("unknown integration exit = %d (%v)", ExitCode(err), err)
	}
}

// TestOpenRoutesThroughTheLauncher pins that open honors the launcher
// setting: "auto" outside tmux launches in place, and an unknown launcher is
// refused when the config loads, before anything starts.
func TestOpenRoutesThroughTheLauncher(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	if _, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	var launched int
	orig := launchHarness
	launchHarness = func(*harness.Launch, bool) error { launched++; return nil }
	t.Cleanup(func() { launchHarness = orig })

	cfgPath := filepath.Join(env.Root, "cfg", "config.toml")
	t.Setenv("TMUX", "")
	testutil.WriteFile(t, cfgPath, "launcher = \"auto\"\ndefault_harness = \"fake\"\n[harness.fake]\ncommand = \"sh\"\n")
	if _, _, err := run(t, "open", "demo"); err != nil || launched != 1 {
		t.Fatalf("auto outside tmux should launch in place: launched=%d err=%v", launched, err)
	}

	testutil.WriteFile(t, cfgPath, "launcher = \"screen\"\ndefault_harness = \"fake\"\n[harness.fake]\ncommand = \"sh\"\n")
	if _, _, err := run(t, "open", "demo"); err == nil || launched != 1 {
		t.Errorf("unknown launcher should be refused: launched=%d err=%v", launched, err)
	}
}

func TestAgentCommandsInferPackageFromCwd(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	out, stderr, err := run(t, "new", "work", "--active", a, "--session", "s1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	dir := strings.TrimSpace(out)
	pkgDir := filepath.Dir(dir)

	// Outside any package, inference fails with not-found.
	if _, _, err := run(t, "note", "orphan"); ExitCode(err) != ExitNotFound {
		t.Errorf("note outside a package: exit %d (%v)", ExitCode(err), err)
	}

	t.Chdir(filepath.Join(dir, "alpha"))
	t.Setenv("VMONO_AUTHOR", "agent:claude")

	if _, _, err := run(t, "note", "decided to freeze v3"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "describe", "--context", "Ticket PLAT-9."); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "role", "alpha", "Owns the gateway"); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, "add", b, "--role", "SDK client"); err != nil {
		t.Fatalf("add: %v\n%s", err, stderr)
	}

	out, _, err = run(t, "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var s core.ShowResult
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("show --json: %v\n%s", err, out)
	}
	if s.Context != "Ticket PLAT-9." || len(s.Notes) != 1 || s.Notes[0].By != "agent:claude" || len(s.Members) != 2 {
		t.Errorf("show = %+v", s)
	}
	if s.Session != "s1" || s.Branch != "s1" || len(s.Sessions) != 1 || s.Members[0].Branch != "s1" {
		t.Errorf("show from inside a session should report it: %+v", s)
	}
	if s.Members[0].Role != "Owns the gateway" || s.Members[1].Role != "SDK client" {
		t.Errorf("roles = %+v", s.Members)
	}
	if text, _, _ := run(t, "show"); !strings.Contains(text, "Ticket PLAT-9.") || !strings.Contains(text, "SDK client") {
		t.Errorf("show text = %q", text)
	}

	// Explicit package argument works from anywhere.
	t.Chdir(env.Root)
	if _, _, err := run(t, "note", "work", "second note"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "role", "work", "beta", "Updated"); err != nil {
		t.Fatal(err)
	}

	// rm: dirty member is refused (exit 2); clean one is removed.
	testutil.WriteFile(t, filepath.Join(dir, "beta", "wip"), "x")
	_, _, err = run(t, "rm", "work", "beta")
	if ExitCode(err) != ExitRefused {
		t.Fatalf("rm dirty: exit %d (%v)", ExitCode(err), err)
	}
	if _, _, err := run(t, "rm", "work", "beta", "--force"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "rm", "work", "beta"); ExitCode(err) != ExitNotFound {
		t.Errorf("rm unknown member: exit %d", ExitCode(err))
	}

	log := testutil.Git(t, pkgDir, "log", "--format=%s")
	for _, want := range []string{"vmono: note (agent:claude)", "vmono: add beta (agent:claude)", "vmono: rm beta (agent:claude)"} {
		if !strings.Contains(log, want) {
			t.Errorf("package log missing %q:\n%s", want, log)
		}
	}
}

func TestDescribeFlags(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	if _, _, err := run(t, "new", "d", "--active", a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "describe", "d"); err == nil {
		t.Error("describe with no flags must fail")
	}
	if _, _, err := run(t, "describe", "d", "--purpose", "x", "--purpose-file", "y"); err == nil {
		t.Error("text and file flags together must fail")
	}
	f := filepath.Join(env.Root, "p.md")
	testutil.WriteFile(t, f, "From file.\n")
	if _, _, err := run(t, "describe", "d", "--purpose-file", f); err != nil {
		t.Fatal(err)
	}
	out, _, _ := run(t, "show", "d", "--json")
	if !strings.Contains(out, "From file.") {
		t.Errorf("purpose not replaced: %s", out)
	}
	if _, _, err := run(t, "describe", "d", "--purpose", ""); err != nil {
		t.Fatal(err)
	}
	if out, _, _ := run(t, "show", "d", "--json"); !strings.Contains(out, `"purpose": ""`) {
		t.Errorf("purpose should be cleared: %s", out)
	}
}

func TestShellInit(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "fish", "pwsh"} {
		out, _, err := run(t, "shell-init", sh)
		if err != nil || !strings.Contains(out, "vcd") || !strings.Contains(out, "vmono path") {
			t.Errorf("%s: %q, %v", sh, out, err)
		}
	}
	if _, _, err := run(t, "shell-init", "tcsh"); err == nil {
		t.Error("unsupported shell must fail")
	}
}

func TestEditAppliesEditorChanges(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	out, _, err := run(t, "new", "ed", "--active", a, "--session", "work")
	if err != nil {
		t.Fatal(err)
	}
	dir := strings.TrimSpace(out)

	orig := runEditor
	t.Cleanup(func() { runEditor = orig })

	// The "editor" appends a member and changes the purpose.
	runEditor = func(file string) error {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		extra := "\n[[member]]\nname = \"beta\"\nsource = '" + b + "'\nmode = \"active\"\nrole = \"from editor\"\n"
		text := strings.Replace(string(data), "purpose", "# touched\npurpose", 1) + extra
		return os.WriteFile(file, []byte(text), 0o600)
	}
	out, stderr, err := run(t, "edit", "ed")
	if err != nil {
		t.Fatalf("edit: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "1 added") {
		t.Errorf("summary = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "beta", "README.md")); err != nil {
		t.Error("member added by editing should exist on disk")
	}

	// A no-op edit says so.
	runEditor = func(string) error { return nil }
	if out, _, err = run(t, "edit", "ed"); err != nil || !strings.Contains(out, "no changes") {
		t.Errorf("no-op edit = %q, %v", out, err)
	}

	// Invalid edits are rejected and the file is kept for the user.
	runEditor = func(file string) error { return os.WriteFile(file, []byte("this is = not valid ["), 0o600) }
	_, _, err = run(t, "edit", "ed")
	if err == nil || !strings.Contains(err.Error(), "your edits are kept at") {
		t.Errorf("invalid edit error = %v", err)
	}
	if _, _, err := run(t, "show", "ed"); err != nil {
		t.Errorf("package must survive a rejected edit: %v", err)
	}
}

func TestSyncCommand(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	out, _, err := run(t, "new", "sy", "--active", a, "--session", "work")
	if err != nil {
		t.Fatal(err)
	}
	dir := strings.TrimSpace(out)
	if out, _, err = run(t, "sync", "sy"); err != nil || !strings.Contains(out, "already in sync") {
		t.Fatalf("clean sync = %q, %v", out, err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "stray"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "sync", "sy", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res []core.SessionSync
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res) != 1 || res[0].Session != "work" || len(res[0].Recreated) != 1 || len(res[0].Unlisted) != 1 {
		t.Errorf("sync --json = %q, %v", out, err)
	}
	if _, _, err := run(t, "sync", "ghost"); ExitCode(err) != ExitNotFound {
		t.Errorf("unknown package: exit %d", ExitCode(err))
	}
}

func TestDoctorAndInspectCommands(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	out, _, err := run(t, "new", "dr", "--active", a, "--session", "work")
	if err != nil {
		t.Fatal(err)
	}
	dir := strings.TrimSpace(out)

	out, _, err = run(t, "inspect", "dr", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var ins core.InspectResult
	if err := json.Unmarshal([]byte(out), &ins); err != nil || len(ins.Members) != 1 || ins.Members[0].GitCommonDir == "" {
		t.Fatalf("inspect = %q, %v", out, err)
	}

	if out, _, err = run(t, "doctor"); err != nil || !strings.Contains(out, "no problems") {
		t.Fatalf("healthy doctor = %q, %v", out, err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "doctor")
	if err == nil || !strings.Contains(out, "missing-worktree") || !strings.Contains(err.Error(), "--fix") {
		t.Fatalf("drift doctor = %q, %v", out, err)
	}
	if ExitCode(err) != ExitError {
		t.Errorf("remaining problems should exit 1, got %d", ExitCode(err))
	}
	out, _, err = run(t, "doctor", "--fix", "--json")
	if err != nil {
		t.Fatalf("doctor --fix: %v\n%s", err, out)
	}
	var res core.DoctorResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Remaining() != 0 {
		t.Errorf("doctor --fix --json = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha", "README.md")); err != nil {
		t.Error("doctor --fix should have recreated the worktree")
	}
}

// TestEveryReadCommandEmitsValidJSON guards the spec's promise that every
// read command takes --json, including on empty state where a nil slice
// would encode as null.
func TestEveryReadCommandEmitsValidJSON(t *testing.T) {
	env := testutil.NewEnv(t)

	// Empty state: no packages, no repos, no source roots.
	for _, args := range [][]string{{"ls", "--json"}, {"repos", "--json"}, {"doctor", "--json"}} {
		out, _, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil || v == nil {
			t.Errorf("%v: invalid or null JSON %q (%v)", args, out, err)
		}
		if strings.Contains(out, "null") {
			t.Errorf("%v: nil slice leaked as null: %s", args, out)
		}
	}

	a := env.NewSource(t, "alpha")
	if _, _, err := run(t, "new", "js", "--active", a); err != nil {
		t.Fatal(err)
	}
	// A package with no sessions still lists, shows and syncs as valid JSON.
	for _, args := range [][]string{{"show", "js", "--json"}, {"session", "ls", "js", "--json"}, {"sync", "js", "--json"}, {"path", "js", "--json"}} {
		out, _, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out, "null") {
			t.Errorf("%v: nil slice leaked as null: %s", args, out)
		}
	}
	if _, _, err := run(t, "session", "new", "js", "work"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ls", "--json"}, {"repos", "--json"}, {"status", "js", "--json"}, {"show", "js", "--json"}, {"show", "js/work", "--json"},
		{"inspect", "js", "--json"}, {"path", "js", "--json"}, {"sync", "js", "--json"}, {"doctor", "--json"}, {"session", "ls", "js", "--json"},
	} {
		out, _, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("%v: invalid JSON %q: %v", args, out, err)
		}
		if strings.Contains(out, ": null") {
			t.Errorf("%v: null field in %s", args, out)
		}
	}
}

func TestNewWithoutSessionMakesOnlyThePackage(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")

	if _, _, err := run(t, "new", "p", "--active", a, "--branch", "x"); err == nil || ExitCode(err) != ExitError {
		t.Errorf("--branch without --session must be rejected: %v", err)
	}
	out, _, err := run(t, "new", "p", "--active", a)
	if err != nil {
		t.Fatal(err)
	}
	dir := strings.TrimSpace(out)
	if filepath.Base(dir) != "p" {
		t.Fatalf("should print the package dir: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha")); err == nil {
		t.Error("a package has no worktrees until a session starts")
	}
	if out, _, _ := run(t, "session", "ls", "p"); !strings.Contains(out, "no sessions") || !strings.Contains(out, "vmono session new p") {
		t.Errorf("session ls on an empty package = %q", out)
	}
	if _, _, err := run(t, "open", "p"); ExitCode(err) != ExitNotFound {
		t.Errorf("opening a package with no sessions: exit %d (%v)", ExitCode(err), err)
	}
}

func TestSessionNewLsRmAndSelection(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	if _, _, err := run(t, "new", "duo", "--active", a+","+b); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := run(t, "session", "new", "duo", "one")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	one := strings.TrimSpace(out)
	out, _, err = run(t, "session", "new", "duo", "two", "--branch", "feat/PLAT-1")
	if err != nil {
		t.Fatal(err)
	}
	two := strings.TrimSpace(out)
	if _, _, err := run(t, "session", "new", "duo", "one"); err == nil {
		t.Error("a duplicate session name must fail")
	}
	if got := testutil.Git(t, filepath.Join(two, "alpha"), "branch", "--show-current"); got != "feat/PLAT-1" {
		t.Errorf("--branch ignored: %q", got)
	}
	if got := testutil.Git(t, filepath.Join(one, "alpha"), "branch", "--show-current"); got != "one" {
		t.Errorf("default branch = %q", got)
	}

	out, _, err = run(t, "session", "ls", "duo", "--json")
	var rows []core.SessionSummary
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 2 {
		t.Fatalf("session ls --json = %q, %v", out, err)
	}
	if text, _, _ := run(t, "session", "ls", "duo"); !strings.Contains(text, "feat/PLAT-1") || !strings.Contains(text, "one") {
		t.Errorf("session ls table = %q", text)
	}
	if text, _, _ := run(t, "ls"); !strings.Contains(text, "duo") {
		t.Errorf("ls = %q", text)
	}

	// A bare package is ambiguous with two sessions; the error lists them.
	var launched []*harness.Launch
	orig := launchHarness
	launchHarness = func(l *harness.Launch, _ bool) error { launched = append(launched, l); return nil }
	t.Cleanup(func() { launchHarness = orig })
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), "default_harness = \"fake\"\n[harness.fake]\ncommand = \"sh\"\n")

	_, _, err = run(t, "open", "duo")
	if ExitCode(err) != ExitNotFound || !strings.Contains(err.Error(), "duo/NAME") || !strings.Contains(err.Error(), "one, two") {
		t.Fatalf("ambiguous open: exit %d, %v", ExitCode(err), err)
	}
	if _, _, err := run(t, "open", "duo/two"); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 1 || launched[0].Dir != two {
		t.Fatalf("launched = %+v", launched)
	}
	var sess, pkg string
	for _, kv := range launched[0].Env {
		switch {
		case strings.HasPrefix(kv, "VMONO_SESSION="):
			sess = strings.TrimPrefix(kv, "VMONO_SESSION=")
		case strings.HasPrefix(kv, "VMONO_PACKAGE="):
			pkg = strings.TrimPrefix(kv, "VMONO_PACKAGE=")
		}
	}
	if sess != "two" || pkg != "duo" {
		t.Errorf("env session=%q package=%q", sess, pkg)
	}

	// From inside a session the session is inferred; from the package dir it is not.
	// This uses os.Chdir rather than t.Chdir on purpose: t.Chdir keeps the
	// directory it leaves open until the test ends, and on Windows an open
	// handle on one/alpha stops the forced removal below from deleting it.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	chdir := func(dir string) {
		t.Helper()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PWD", dir) // what t.Chdir does, so symlinked temp dirs resolve as before
	}
	chdir(filepath.Join(one, "alpha"))
	if out, _, err := run(t, "status", "--json"); err != nil || !strings.Contains(out, `"session": "one"`) {
		t.Errorf("status inside a session = %q, %v", out, err)
	}
	if out, _, _ := run(t, "path"); strings.TrimSpace(out) != one {
		t.Errorf("path inside a session = %q, want %q", out, one)
	}
	chdir(filepath.Dir(one))
	if _, _, err := run(t, "status"); ExitCode(err) != ExitNotFound {
		t.Errorf("status in a package dir with two sessions: exit %d", ExitCode(err))
	}
	if out, _, _ := run(t, "path"); strings.TrimSpace(out) != filepath.Dir(one) {
		t.Errorf("path in a package dir = %q", out)
	}

	// A dirty session refuses removal (exit 2) without touching the other.
	chdir(env.Root)
	testutil.WriteFile(t, filepath.Join(one, "alpha", "wip"), "x")
	_, _, err = run(t, "session", "rm", "duo/one")
	if ExitCode(err) != ExitRefused || !strings.Contains(err.Error(), "one/alpha") {
		t.Fatalf("dirty session rm: exit %d, %v", ExitCode(err), err)
	}
	if out, _, err := run(t, "session", "rm", "duo/one", "--force"); err != nil || !strings.Contains(out, "removed session duo/one") {
		t.Fatalf("forced session rm = %q, %v", out, err)
	}
	if _, err := os.Stat(one); !os.IsNotExist(err) {
		t.Error("session dir should be gone")
	}
	if _, err := os.Stat(filepath.Join(two, "alpha", "README.md")); err != nil {
		t.Error("the other session must survive")
	}
	// Now there is exactly one, so the bare package form works again.
	if _, _, err := run(t, "open", "duo"); err != nil {
		t.Errorf("open of a package with one session: %v", err)
	}
}

func TestPackageChangesReachEverySessionFromTheCLI(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	if _, _, err := run(t, "new", "wide", "--active", a, "--session", "one"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "session", "new", "wide", "two")
	if err != nil {
		t.Fatal(err)
	}
	two := strings.TrimSpace(out)
	one := filepath.Join(filepath.Dir(two), "one")

	if _, _, err := run(t, "note", "wide", "shared learning"); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := run(t, "add", "wide", b)
	if err != nil {
		t.Fatalf("add: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "2 session(s)") {
		t.Errorf("add output = %q", out)
	}
	for _, dir := range []string{one, two} {
		agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		if !strings.Contains(string(agents), "shared learning") || !strings.Contains(string(agents), "beta") {
			t.Errorf("%s AGENTS.md misses the package change:\n%s", dir, agents)
		}
		if _, err := os.Stat(filepath.Join(dir, "beta", "README.md")); err != nil {
			t.Errorf("%s misses the new member's worktree", dir)
		}
	}
	// Package sync covers both sessions; a session sync only its own.
	if err := os.RemoveAll(filepath.Join(one, "beta")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(two, "beta")); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, "sync", "wide/one", "--json")
	var res []core.SessionSync
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || len(res) != 1 || res[0].Session != "one" {
		t.Fatalf("session sync = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(two, "beta")); err == nil {
		t.Error("syncing one session must not touch the other")
	}
	out, _, err = run(t, "sync", "wide", "--json")
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || len(res) != 2 {
		t.Fatalf("package sync = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(two, "beta", "README.md")); err != nil {
		t.Error("package sync should rebuild every session")
	}
}

func TestBranchFlagsBelongToASession(t *testing.T) {
	env := testutil.NewEnv(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	for _, flag := range [][]string{{"--prefix", "feat"}, {"--branch", "x"}, {"--track", "x"}, {"--from", "x"}, {"--reuse-branch"}} {
		if _, _, err := run(t, append([]string{"new", "demo", "--active", a}, flag...)...); err == nil {
			t.Errorf("%v without --session must be rejected", flag)
		}
	}
	if _, stderr, err := run(t, "new", "demo", "--active", a+","+b, "--session", "work", "--prefix", "feat", "--branch", "beta=other"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for member, want := range map[string]string{"alpha": "feat/work", "beta": "other"} {
		if got := testutil.Git(t, filepath.Join(env.PackageRoot, "demo", "work", member), "branch", "--show-current"); got != want {
			t.Errorf("%s is on %q, want %q", member, got, want)
		}
	}
	out, _, _ := run(t, "session", "ls", "demo", "--json")
	if !strings.Contains(out, `"state": "in-progress"`) {
		t.Errorf("ls --json = %s", out)
	}
	for name, args := range map[string][]string{
		"two branches for all": {"--branch", "a", "--branch", "b"},
		"empty member":         {"--branch", "=x"},
		"empty branch":         {"--from", "alpha="},
		"unknown member":       {"--branch", "ghost=x"},
	} {
		if _, _, err := run(t, append([]string{"session", "new", "demo", "two"}, args...)...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSessionNewFrom(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "branch", "parent")
	if _, stderr, err := run(t, "new", "demo", "--active", a, "--session", "child", "--from", "parent"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if out, _, err := run(t, "status", "demo/child", "--json"); err != nil || !strings.Contains(out, `"built_on": "parent"`) {
		t.Errorf("status = %s, %v", out, err)
	}
	if _, _, err := run(t, "session", "new", "demo", "two", "--from", "alpha=nope"); ExitCode(err) != ExitNotFound {
		t.Errorf("missing ref = %v", err)
	}
}

func TestRemovedSessionCommandsSayWhatToUseInstead(t *testing.T) {
	testutil.NewEnv(t)
	for _, args := range [][]string{{"session", "finish", "--json"}, {"session", "finish", "demo/work", "--close"}, {"session", "into", "demo/work", "--all", "x"}} {
		_, _, err := run(t, args...)
		if ExitCode(err) != ExitError || !strings.Contains(err.Error(), "was removed") || !strings.Contains(err.Error(), "vmono session") {
			t.Errorf("%v: %v", args, err)
		}
	}
	out, _, err := run(t, "session", "--help")
	if err != nil || strings.Contains(out, "finish") || strings.Contains(out, "  into ") {
		t.Errorf("removed commands should not be listed (%v):\n%s", err, out)
	}
}

func TestHookSessionStart(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	out, stderr, err := run(t, "new", "work", "--active", a, "--ref", b, "--session", "s1")
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	dir := strings.TrimSpace(out)

	// From inside a member of the session it reports the session and every
	// member, and names the commands the plugin offers.
	t.Chdir(filepath.Join(dir, "alpha"))
	testutil.WriteFile(t, filepath.Join(dir, "alpha", "wip"), "x")
	got, _, err := run(t, "hook", "session-start")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"work/s1", "s1", "in-progress", "alpha", "beta", "1 untracked", "/vmono:finish", "/vmono:note", "/vmono:status"} {
		if !strings.Contains(got, want) {
			t.Errorf("hook output missing %q:\n%s", want, got)
		}
	}

	// The status command still prints the same table the hook embeds.
	statusOut, _, err := run(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, statusOut) {
		t.Errorf("hook should embed the status text.\nstatus:\n%s\nhook:\n%s", statusOut, got)
	}

	// It stays quiet, and succeeds, wherever it is not in a session: a
	// package directory with exactly one session, and an unrelated one.
	for _, where := range []string{filepath.Dir(dir), env.Root} {
		t.Chdir(where)
		got, stderr, err := run(t, "hook", "session-start")
		if err != nil || got != "" || stderr != "" {
			t.Errorf("outside a session (%s): out %q, stderr %q, err %v", where, got, stderr, err)
		}
	}
}

func TestHookIsHiddenFromHelp(t *testing.T) {
	testutil.NewEnv(t)
	out, _, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "session-start") || strings.Contains(out, "  hook ") {
		t.Errorf("hook should not appear in help:\n%s", out)
	}
}

func TestSessionPush(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	out, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}
	wt := filepath.Join(strings.TrimSpace(out), "alpha")
	testutil.WriteFile(t, filepath.Join(wt, "one.txt"), "one\n")
	testutil.Git(t, wt, "add", "-A")
	testutil.Git(t, wt, "commit", "--quiet", "-m", "add one.txt")
	remote := filepath.Join(env.Root, "remotes", "alpha.git")

	testutil.WriteFile(t, filepath.Join(wt, "forgotten.txt"), "x\n")
	branch := testutil.Git(t, wt, "branch", "--show-current")
	before := testutil.Git(t, remote, "rev-parse", "refs/heads/main")

	text, _, err := run(t, "session", "push", "demo/work", "--dry-run")
	if err != nil || !strings.Contains(text, "would push 1 commit of "+branch+" to origin/"+branch) || !strings.Contains(text, "note: ") || !strings.Contains(text, "dry run") {
		t.Errorf("dry run = %q, %v", text, err)
	}
	if review, _, err := run(t, "session", "review", "demo/work"); err != nil || review != text {
		t.Errorf("review = %q, %v; want the output of push --dry-run", review, err)
	}
	var plan core.PushResult
	if out, _, err := run(t, "session", "review", "demo/work", "--json"); err != nil || json.Unmarshal([]byte(out), &plan) != nil || len(plan.Members) != 1 || plan.Members[0].Status != core.PushReady {
		t.Errorf("review --json = %q, %v", out, err)
	}
	if refs := testutil.Git(t, remote, "for-each-ref", "refs/heads/"+branch); refs != "" {
		t.Fatalf("a dry run must not push: %s", refs)
	}
	if _, _, err := run(t, "session", "push", "demo/work", "--member", "nope"); err == nil {
		t.Error("an unknown --member must be rejected")
	}

	out, _, err = run(t, "session", "push", "demo/work", "--json")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	var res core.PushResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("push --json: %v\n%s", err, out)
	}
	if len(res.Members) != 1 || res.Members[0].Status != core.PushPushed || res.Members[0].Commits != 1 {
		t.Errorf("result = %+v", res)
	}
	if testutil.Git(t, remote, "rev-parse", "refs/heads/"+branch) != testutil.Git(t, a, "rev-parse", branch) {
		t.Error("the remote should have the session branch")
	}
	if testutil.Git(t, remote, "rev-parse", "refs/heads/main") != before {
		t.Error("main must not move")
	}
	if text, _, err := run(t, "session", "push", "demo/work"); err != nil || !strings.Contains(text, "nothing to push") {
		t.Errorf("pushing again = %q, %v", text, err)
	}

	// A blocked member fails the review as it would the push.
	testutil.Git(t, remote, "update-ref", "-d", "refs/heads/"+branch)
	testutil.Git(t, a, "fetch", "--quiet", "--prune")
	if text, _, err := run(t, "session", "review", "demo/work"); ExitCode(err) != ExitRefused || !strings.Contains(text, "was deleted") {
		t.Errorf("review of a blocked member = %q, %v", text, err)
	}
}

func TestSessionUpdate(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	out, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}
	wt := filepath.Join(strings.TrimSpace(out), "alpha")
	testutil.WriteFile(t, filepath.Join(a, "new.txt"), "new\n")
	testutil.Git(t, a, "add", "-A")
	testutil.Git(t, a, "commit", "--quiet", "-m", "new")
	testutil.Git(t, a, "push", "--quiet", "origin", "main")

	testutil.WriteFile(t, filepath.Join(wt, "README.md"), "edited\n")
	out, _, err = run(t, "session", "update", "demo/work", "--json")
	var res core.UpdateResult
	if ExitCode(err) != ExitRefused || json.Unmarshal([]byte(out), &res) != nil || res.Members[0].Status != core.UpdateSkipped {
		t.Errorf("dirty update = %q, %v", out, err)
	}
	testutil.Git(t, wt, "checkout", "--quiet", "README.md")
	out, _, err = run(t, "session", "update", "demo/work")
	if err != nil || !strings.Contains(out, "rebased onto origin/main (1 behind") {
		t.Errorf("update = %q, %v", out, err)
	}
}

func TestSessionNewTrack(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/x")
	if _, _, err := run(t, "new", "demo", "--active", a, "--session", "one", "--track", "feat/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, filepath.Join(env.PackageRoot, "demo", "one", "alpha"), "rev-parse", "HEAD"), testutil.Git(t, a, "rev-parse", "origin/feat/x"); got != want {
		t.Errorf("the session starts at %s, want %s", got, want)
	}
	if _, _, err := run(t, "session", "new", "demo", "two", "--track", "alpha=nope"); ExitCode(err) != ExitNotFound {
		t.Errorf("missing branch = %v", err)
	}
}

func TestNoHooksFlag(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), "[hooks.pre_remove]\ncommand = \"exit 1\"\non_failure = \"abort\"\n")
	if _, _, err := run(t, "new", "demo", "--active", a, "--session", "work"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "session", "rm", "demo/work"); ExitCode(err) != ExitRefused {
		t.Errorf("want a veto, got %v", err)
	}
	if _, _, err := run(t, "session", "rm", "demo/work", "--no-hooks"); err != nil {
		t.Errorf("--no-hooks: %v", err)
	}
}

// TestUninstallKeepsALauncherAnotherIntegrationUses covers tmux and herdr
// sharing the auto launcher.
func TestUninstallKeepsALauncherAnotherIntegrationUses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("herdr is not offered on Windows")
	}
	testutil.NewEnv(t)
	t.Setenv("HERDR_CONFIG_PATH", "")
	for _, args := range [][]string{{"install", "tmux"}, {"install", "herdr"}, {"uninstall", "tmux"}} {
		if _, _, err := run(t, append([]string{"integration"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load()
	if err != nil || cfg.Launcher != config.LauncherAuto {
		t.Errorf("launcher = %q, %v", cfg.Launcher, err)
	}
	if _, _, err := run(t, "integration", "uninstall", "herdr"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ = config.Load(); cfg.Launcher != config.LauncherDirect {
		t.Errorf("launcher = %q", cfg.Launcher)
	}
	if _, _, err := run(t, "integration", "install", "tmux", "--key", "x"); err == nil {
		t.Error("--key is only for herdr")
	}
}
