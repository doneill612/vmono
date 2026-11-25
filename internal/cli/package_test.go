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
	"github.com/doneill612/vmono/internal/herdr"
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

func TestPackageSuggestionsAreReviewedAfterInit(t *testing.T) {
	var notified [][]string
	origHerdr, origReview, origTerm := herdr.Run, runReview, interactive
	t.Cleanup(func() { herdr.Run, runReview, interactive = origHerdr, origReview, origTerm })
	herdr.Run = func(args ...string) ([]byte, error) {
		notified = append(notified, args)
		return nil, errors.New("herdr is not listening")
	}
	stop := false
	initEnv(t, func(l *harness.Launch) error {
		file := filepath.Join(t.TempDir(), "r.json")
		testutil.WriteFile(t, file, `{"overview":"o","suggestions":[{"member":"alpha","kind":"setup","command":"make deps","reason":"Makefile"},{"member":"alpha","kind":"teardown","command":"make down"}]}`)
		for range 2 {
			if out, _, err := run(t, "package", "record", "demo", file); err != nil || !strings.Contains(out, "2 suggested commands") {
				t.Errorf("record = %q %v", out, err)
			}
		}
		if stop {
			return harness.ErrStopped
		}
		return nil
	})
	t.Setenv("HERDR_ENV", "1")

	// With no terminal, it says what there is to do.
	interactive = func() bool { return false }
	reviewed := 0
	runReview = func(c *core.Core, pkg, notice string) error {
		reviewed++
		p, err := c.Load(pkg)
		if err != nil {
			return err
		}
		if c.FindInit(p) != nil || !strings.Contains(notice, "recorded") {
			t.Errorf("the review comes after the cleanup, with the summary: %q", notice)
		}
		return c.ReviewSuggestions(p, []core.SuggestionDecision{{ID: p.M.Pending()[0].ID(), Accept: true}})
	}
	out, _, err := run(t, "package", "init", "demo")
	if err != nil || reviewed != 0 || !strings.Contains(out, "2 suggested commands to review") {
		t.Errorf("init = %q %v, reviewed %d", out, err, reviewed)
	}
	if len(notified) != 1 || notified[0][0] != "notification" || !strings.Contains(notified[0][2], "demo: 2 suggested commands") {
		t.Errorf("one notification for new suggestions, none for repeats: %v", notified)
	}

	interactive = func() bool { return true }
	stop = true
	if _, _, err = run(t, "package", "init", "demo"); err == nil || reviewed != 0 {
		t.Errorf("nobody is there to review when vmono was stopped: %v", err)
	}
	stop = false
	if out, _, err = run(t, "package", "init", "demo"); err != nil || reviewed != 1 || !strings.Contains(out, "1 suggested command to review") {
		t.Errorf("init = %q %v, reviewed %d", out, err, reviewed)
	}
	if out, _, _ = run(t, "show", "demo", "--json"); !strings.Contains(out, "make deps") {
		t.Errorf("show = %s", out)
	}

	var list []suggestionJSON
	out, _, _ = run(t, "package", "suggestions", "demo", "--json")
	if json.Unmarshal([]byte(out), &list) != nil || len(list) != 1 || list[0].Command != "make down" {
		t.Fatalf("suggestions = %q", out)
	}
	if out, _, _ = run(t, "package", "suggestions", "demo"); !strings.Contains(out, "$ make down") {
		t.Errorf("suggestions = %q", out)
	}
	t.Setenv("VMONO_AUTHOR", "agent:claude")
	for _, flag := range [][]string{{"--accept", list[0].ID}, {"--dismiss", list[0].ID}, {"--review"}} {
		if _, _, err := run(t, append([]string{"package", "suggestions", "demo"}, flag...)...); !errors.Is(err, core.ErrAgentReview) {
			t.Errorf("%v by an agent: %v", flag, err)
		}
	}
	t.Setenv("VMONO_AUTHOR", "")
	if _, _, err := run(t, "package", "suggestions", "demo", "--accept", "nope"); ExitCode(err) != ExitNotFound {
		t.Errorf("unknown id: %v", err)
	}
	if _, _, err := run(t, "package", "suggestions", "demo", "--dismiss", list[0].ID); err != nil {
		t.Error(err)
	}
	if out, _, _ = run(t, "package", "suggestions", "demo"); !strings.Contains(out, "nothing to review") {
		t.Errorf("suggestions = %q", out)
	}
}
