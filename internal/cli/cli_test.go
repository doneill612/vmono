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
