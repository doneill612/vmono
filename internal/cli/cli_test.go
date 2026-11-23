package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

// TestVersionFlag checks that --version prints the build's version, which is
// "dev" for a test binary.
func TestVersionFlag(t *testing.T) {
	out, _, err := run(t, "--version")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); !strings.HasPrefix(got, "vmono version ") || strings.HasSuffix(got, "version") {
		t.Fatalf("--version = %q, want \"vmono version <version>\"", got)
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
	testutil.NewEnv(t)
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"path", "ghost"}, ExitNotFound},
		{[]string{"status", "ghost"}, ExitNotFound},
		{[]string{"delete", "ghost"}, ExitNotFound},
		{[]string{"new", "x", "--active", "no-such-repo"}, ExitNotFound},
		{[]string{"new", "x"}, ExitError},
		{[]string{"new", "Bad_Name", "--active", "."}, ExitError},
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
	if s.Session != "s1" || s.Branch != "vmono/work/s1" || len(s.Sessions) != 1 || s.Members[0].Branch != "vmono/work/s1" {
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
	if got := testutil.Git(t, filepath.Join(one, "alpha"), "branch", "--show-current"); got != "vmono/duo/one" {
		t.Errorf("default branch = %q", got)
	}

	out, _, err = run(t, "session", "ls", "duo", "--json")
	var rows []core.SessionSummary
	if err != nil || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 2 {
		t.Fatalf("session ls --json = %q, %v", out, err)
	}
	if text, _, _ := run(t, "session", "ls", "duo"); !strings.Contains(text, "feat/PLAT-1") || !strings.Contains(text, "vmono/duo/one") {
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

func TestSessionFinish(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	out, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work")
	if err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}
	wt := filepath.Join(strings.TrimSpace(out), "alpha")
	commit := func(name string) {
		testutil.WriteFile(t, filepath.Join(wt, name), name+"\n")
		testutil.Git(t, wt, "add", "-A")
		testutil.Git(t, wt, "commit", "--quiet", "-m", "add "+name)
	}
	commit("one.txt")

	if text, _, err := run(t, "status", "demo/work"); err != nil || !strings.Contains(text, "in-progress") || !strings.Contains(text, "→ main: 1 to land") {
		t.Errorf("status before finish = %q, %v", text, err)
	}
	text, _, err := run(t, "session", "finish", "demo/work", "--dry-run")
	if err != nil || !strings.Contains(text, "would land 1 commit on main") || !strings.Contains(text, "dry run") {
		t.Errorf("dry run = %q, %v", text, err)
	}
	if got := testutil.Git(t, a, "rev-parse", "main"); got == testutil.Git(t, wt, "rev-parse", "HEAD") {
		t.Fatal("a dry run must not move main")
	}

	out, _, err = run(t, "session", "finish", "demo/work", "--json")
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	var res core.FinishResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("finish --json: %v\n%s", err, out)
	}
	if !res.Finished || len(res.Members) != 1 || res.Members[0].Status != core.LandLanded || res.Members[0].Into != "main" {
		t.Errorf("result = %+v", res)
	}
	if testutil.Git(t, a, "rev-parse", "main") != testutil.Git(t, wt, "rev-parse", "HEAD") {
		t.Error("main should be at the session tip")
	}
	if text, _, _ := run(t, "session", "ls", "demo"); !strings.Contains(text, "finished") {
		t.Errorf("session ls should show the state: %q", text)
	}

	// A finished session stays listed, and new work reopens it.
	commit("two.txt")
	if text, _, _ := run(t, "session", "ls", "demo"); !strings.Contains(text, "in-progress") {
		t.Errorf("new work should reopen the session: %q", text)
	}

	// Inside the session no argument is needed; uncommitted work is refused
	// with exit code 2 and the reasons on stdout as JSON.
	// This uses os.Chdir rather than t.Chdir on purpose: t.Chdir keeps the
	// directory it leaves open until the test ends, and on Windows that open
	// handle stops the --close removal below from deleting the worktree.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })
	chdir := func(dir string) {
		t.Helper()
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PWD", dir) // what t.Chdir does, so symlinked temp dirs resolve as before
	}
	chdir(wt)
	testutil.WriteFile(t, filepath.Join(wt, "forgotten.txt"), "x")
	out, _, err = run(t, "session", "finish", "--json")
	if ExitCode(err) != ExitRefused {
		t.Fatalf("exit code = %d (%v), want %d", ExitCode(err), err, ExitRefused)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Members[0].Status != core.LandBlocked || res.Finished {
		t.Errorf("refused result = %+v (%v)\n%s", res, err, out)
	}
	if err := os.Remove(filepath.Join(wt, "forgotten.txt")); err != nil {
		t.Fatal(err)
	}
	// Windows cannot delete the working directory, so step out and name the
	// session explicitly.
	chdir(env.Root)
	if text, _, err := run(t, "session", "finish", "demo/work", "--close"); err != nil || !strings.Contains(text, "removed session demo/work") {
		t.Errorf("finish --close = %q, %v", text, err)
	}
}

func TestIntoBelongsToASession(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	if _, _, err := run(t, "new", "demo", "--active", a, "--into", "feature"); err == nil {
		t.Error("--into without --session must be rejected")
	}
	if _, stderr, err := run(t, "new", "demo", "--active", a, "--session", "work", "--into", "feature"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	out, _, _ := run(t, "session", "ls", "demo", "--json")
	if !strings.Contains(out, `"state": "in-progress"`) {
		t.Errorf("ls --json = %s", out)
	}
	data, err := os.ReadFile(filepath.Join(env.PackageRoot, "demo", "work", ".vmono", "session.toml"))
	if err != nil || !strings.Contains(string(data), "[into]\nalpha = \"feature\"") {
		t.Errorf("session.toml = %q, %v", data, err)
	}
}

func TestSessionIntoIsPerMember(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	testutil.Git(t, b, "checkout", "--quiet", "-b", "develop")
	if _, stderr, err := run(t, "new", "demo", "--active", a+","+b, "--session", "work"); err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}

	read := func() map[string]string {
		t.Helper()
		out, _, err := run(t, "session", "into", "demo/work", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var rows []struct {
			Member string `json:"member"`
			Into   string `json:"into"`
		}
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got := map[string]string{}
		for _, r := range rows {
			got[r.Member] = r.Into
		}
		return got
	}
	if got := read(); got["alpha"] != "main" || got["beta"] != "develop" || len(got) != 2 {
		t.Errorf("each member should land where its source was checked out: %v", got)
	}

	text, _, err := run(t, "session", "into", "demo/work", "--all", "release", "--member", "beta=hotfix")
	if err != nil || !strings.Contains(text, "alpha") || !strings.Contains(text, "release") || !strings.Contains(text, "hotfix") {
		t.Errorf("session into = %q, %v", text, err)
	}
	if got := read(); got["alpha"] != "release" || got["beta"] != "hotfix" {
		t.Errorf("after the change: %v", got)
	}

	for name, args := range map[string][]string{
		"unknown member":       {"session", "into", "demo/work", "--member", "ghost=x"},
		"member without =":     {"session", "into", "demo/work", "--member", "beta"},
		"two branches for all": {"session", "finish", "demo/work", "--dry-run", "--into", "a", "--into", "b"},
		"empty member":         {"session", "finish", "demo/work", "--dry-run", "--into", "=x"},
	} {
		if _, _, err := run(t, args...); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err := run(t, "new", "other", "--active", a, "--into", "alpha=x"); err == nil {
		t.Error("--into without --session must still be rejected")
	}
	if _, stderr, err := run(t, "session", "new", "demo", "second", "--into", "beta=feature/y"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	out, _, _ := run(t, "session", "into", "demo/second", "--json")
	if !strings.Contains(out, "feature/y") || !strings.Contains(out, "main") {
		t.Errorf("per-member --into at create: %s", out)
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
	for _, want := range []string{"work/s1", "vmono/work/s1", "in-progress", "alpha", "beta", "1 untracked", "/vmono:finish", "/vmono:note", "/vmono:status"} {
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
