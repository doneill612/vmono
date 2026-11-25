package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/testutil"
)

// initEnv makes a package "demo" and runs fn in place of its harness.
func initEnv(t *testing.T, fn func(l *harness.Launch) error) *testutil.Env {
	t.Helper()
	env := testutil.NewEnv(t)
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), "default_harness = \"fake\"\n[harness.fake]\ncommand = \"git\"\ninit_args = [\"{prompt}\"]\n")
	if _, stderr, err := run(t, "new", "demo", "--active", env.NewSource(t, "alpha")); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	orig := runInitHarness
	runInitHarness = func(l *harness.Launch, _ bool) error { return fn(l) }
	t.Cleanup(func() { runInitHarness = orig })
	return env
}

func TestPackageInitAndRecord(t *testing.T) {
	env := initEnv(t, func(l *harness.Launch) error {
		file := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(l.Dir))), "record.json")
		testutil.WriteFile(t, file, `{"purpose":"why","overview":"the overview","roles":{"alpha":"does things"}}`)
		wd, _ := os.Getwd()
		t.Chdir(l.Dir) // as the agent does: the package is inferred
		defer t.Chdir(wd)
		out, _, err := run(t, "package", "record", file, "--json")
		var res core.RecordResult
		if err != nil || json.Unmarshal([]byte(out), &res) != nil || len(res.Set) != 3 {
			t.Errorf("record = %q %v", out, err)
		}
		return nil
	})
	out, stderr, err := run(t, "package", "init", "demo")
	if err != nil || !strings.Contains(out, "recorded an overview of demo") {
		t.Fatalf("init = %q %v\n%s", out, err, stderr)
	}
	if out, _, _ = run(t, "show", "demo"); !strings.Contains(out, "the overview") || !strings.Contains(out, "does things") || strings.Contains(out, "out of date") {
		t.Errorf("show = %q", out)
	}
	if out, _, _ = run(t, "add", "demo", env.NewSource(t, "beta")); !strings.Contains(out, "overview out of date (+beta)") {
		t.Errorf("add should hint at a rerun: %q", out)
	}
	if out, _, _ = run(t, "package", "init", "demo", "--clean"); !strings.Contains(out, "nothing to clean") {
		t.Errorf("clean = %q", out)
	}
	if _, _, err := run(t, "describe", "demo", "--overview", ""); err != nil {
		t.Fatal(err)
	}
	if out, _, _ = run(t, "show", "demo", "--json"); strings.Contains(out, `"init"`) {
		t.Errorf("cleared overview still shows: %q", out)
	}
}

func TestPackageInitExitCodesAndClean(t *testing.T) {
	initEnv(t, func(l *harness.Launch) error {
		testutil.WriteFile(t, filepath.Join(l.Dir, "_ref", "alpha", "x"), "x")
		return &harness.ExitError{Code: 5}
	})
	if _, _, err := run(t, "package", "init", "demo"); ExitCode(err) != 5 {
		t.Errorf("the harness's status should come through: %v", err)
	}
	if _, _, err := run(t, "package", "init", "demo"); ExitCode(err) != ExitRefused {
		t.Errorf("changes left behind: %v", err)
	}
	if _, _, err := run(t, "package", "init", "demo", "--clean"); ExitCode(err) != ExitRefused {
		t.Errorf("clean without force: %v", err)
	}
	if _, _, err := run(t, "package", "init", "demo", "--clean", "--force"); err != nil {
		t.Error(err)
	}
	if _, _, err := run(t, "package", "init", "ghost"); ExitCode(err) != ExitNotFound {
		t.Errorf("unknown package: %v", err)
	}
	if _, _, err := run(t, "package", "init", "demo", "--force"); err == nil {
		t.Error("--force alone should be refused")
	}
}

func TestPackageRecordRefusesBadInput(t *testing.T) {
	env := initEnv(t, func(*harness.Launch) error { return errors.New("unused") })
	for text, code := range map[string]int{
		"not json":                               ExitError,
		`{"overview":"x","surprise":1}`:          ExitError,
		`{"overview":""}`:                        ExitError,
		`{"overview":"x","roles":{"ghost":"r"}}`: ExitNotFound,
	} {
		file := filepath.Join(env.Root, "r.json")
		testutil.WriteFile(t, file, text)
		if _, _, err := run(t, "package", "record", "demo", file); ExitCode(err) != code {
			t.Errorf("%s: exit %d (%v)", text, ExitCode(err), err)
		}
	}
}
