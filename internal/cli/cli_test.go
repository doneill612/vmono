package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestNewLsStatusPathDelete(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	purpose := filepath.Join(env.Root, "purpose.md")
	testutil.WriteFile(t, purpose, "Do the thing.\n")

	out, stderr, err := run(t, "new", "demo", "--active", a, "--ref", b, "--purpose-file", purpose)
	if err != nil {
		t.Fatalf("new: %v\n%s", err, stderr)
	}
	dir := strings.TrimSpace(out)
	if !strings.HasSuffix(dir, "demo") {
		t.Fatalf("new should print the package path, got %q", out)
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
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) != 1 || list[0].Members != 2 {
		t.Fatalf("ls --json = %q, %v", out, err)
	}
	if out, _, _ = run(t, "ls"); !strings.Contains(out, "demo") {
		t.Errorf("ls table missing package: %q", out)
	}

	out, _, err = run(t, "status", "demo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st core.PackageStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil || len(st.Members) != 2 {
		t.Fatalf("status --json = %q, %v", out, err)
	}
	if out, _, _ = run(t, "status", "demo"); !strings.Contains(out, "clean") {
		t.Errorf("status table = %q", out)
	}

	if out, _, _ = run(t, "path", "demo"); strings.TrimSpace(out) != dir {
		t.Errorf("path = %q, want %q", out, dir)
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
	data, err := os.ReadFile(filepath.Join(env.Root, "cfg", "config.toml"))
	if err != nil || !strings.Contains(string(data), root) {
		t.Fatalf("config not written: %v\n%s", err, data)
	}
}

func TestOpenPassesArgsAndHarness(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	if _, stderr, err := run(t, "new", "demo", "--active", a); err != nil {
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
	if !strings.HasSuffix(got.Dir, "demo") {
		t.Errorf("dir = %q", got.Dir)
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
	out, stderr, err := run(t, "new", "work", "--active", a)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	dir := strings.TrimSpace(out)

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

	log := testutil.Git(t, dir, "log", "--format=%s")
	for _, want := range []string{"vmono: note (agent:claude)", "vmono: add beta (agent:claude)", "vmono: rm beta (agent:claude)"} {
		if !strings.Contains(log, want) {
			t.Errorf("shell log missing %q:\n%s", want, log)
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
	out, _, err := run(t, "new", "ed", "--active", a)
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
		extra := "\n[[member]]\nname = \"beta\"\nsource = \"" + b + "\"\nmode = \"active\"\nrole = \"from editor\"\n"
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
