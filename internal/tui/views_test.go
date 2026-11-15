package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/testutil"
)

// goToView runs a command-bar command.
func goToView(d *driver, cmd string) {
	d.key(":")
	d.typeText(cmd)
	d.key("enter")
}

func TestReposViewListsReposWithUsage(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	env.NewSource(t, "beta")
	t.Setenv("VMONO_SOURCE_ROOTS", filepath.Dir(a))
	seed(t, env, "payments", a)
	d := newDriver(t, env, "")

	goToView(d, "repos")
	d.eventually(func() bool { return containsView(d, "beta") }, "repo list")
	d.has("repos[2]", "alpha", "beta", "USED BY", "<repos>")
	m := d.app.top().(*reposModel)
	used := map[string]string{}
	for _, r := range m.tb.rows {
		used[r.cells[repoColName].text] = r.cells[repoColUsed].text
	}
	if used["alpha"] != "1" || used["beta"] != "0" {
		t.Errorf("used by = %v", used)
	}
}

func TestReposEnterScopesPackagesAndNStartsNewPackage(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	t.Setenv("VMONO_SOURCE_ROOTS", filepath.Dir(a))
	seed(t, env, "payments", a)
	seed(t, env, "auth", b)
	d := newDriver(t, env, "")

	goToView(d, "repos")
	d.eventually(func() bool { return containsView(d, "beta") }, "repo list")
	d.key("enter") // alpha
	d.has("packages(alpha)[1]", "payments")
	d.lacks("auth ")
	d.key("esc")
	d.has("repos[2]")

	d.key("n")
	d.has("new package", "alpha", "active") // preselected as an active member
	d.key("esc")
	d.has("repos[2]")
}

func TestReposFindAddsAndSelectsRepo(t *testing.T) {
	env := testutil.NewEnv(t)
	elsewhere := env.NewSource(t, "elsewhere") // no source root configured
	d := newDriver(t, env, "")
	goToView(d, "repos")
	d.has("no source root is configured")
	d.key("a")
	d.key("ctrl+u")
	d.typeText(elsewhere)
	d.key("enter")
	d.has("added elsewhere", "repos[1]")
}

func TestHarnessesViewSetsDefault(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	d := newDriver(t, env, "")
	goToView(d, "harnesses")
	d.has("harnesses[2]", "fake", "missing", "INSTALLED")

	d.key("j", "s") // missing is not on PATH
	d.has("is not on PATH")
	d.key("k", "s") // fake
	d.has("default harness is now fake")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultHarness != "fake" {
		t.Errorf("saved default = %q", cfg.DefaultHarness)
	}
}

func TestHarnessesDefaultChangesWhenSettingAnother(t *testing.T) {
	env := testutil.NewEnv(t)
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), `
default_harness = "one"

[harness.one]
command = "sh"

[harness.two]
command = "sh"
`)
	d := newDriver(t, env, "")
	goToView(d, "hs")
	d.key("j", "s")
	d.has("default harness is now two")
	cfg, _ := config.Load()
	if cfg.DefaultHarness != "two" {
		t.Errorf("saved default = %q", cfg.DefaultHarness)
	}
	d.key("esc")
	d.has("two") // the header shows the new default
}
