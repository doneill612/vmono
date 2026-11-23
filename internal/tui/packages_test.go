package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// seed creates a package named name with the given sources as active members,
// and one session named "work" on its default branch, vmono/<name>/work.
func seed(t *testing.T, env *testutil.Env, name string, sources ...string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Name: name, Purpose: "Purpose of " + name}
	for _, s := range sources {
		m.Members = append(m.Members, manifest.Member{Source: s, Mode: manifest.ModeActive, Role: "role"})
	}
	p, err := c.CreatePackage(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSession(p, "work", core.SessionOptions{}); err != nil {
		t.Fatal(err)
	}
}

// seedPackage creates a package with no sessions.
func seedPackage(t *testing.T, env *testutil.Env, name string, sources ...string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Name: name, Purpose: "Purpose of " + name}
	for _, s := range sources {
		m.Members = append(m.Members, manifest.Member{Source: s, Mode: manifest.ModeActive, Role: "role"})
	}
	if _, err := c.CreatePackage(m); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunSetupWritesConfigAndOpensHome(t *testing.T) {
	env := testutil.NewEnv(t)
	cfg, _ := config.Load()
	c, _ := core.New(cfg) // no config file: Loaded is false
	d := newDriverFor(t, c, "")
	d.has("Welcome to vmono", "Source root", "Package root", "Harnesses")

	d.key("enter") // source root -> package root
	d.key("enter") // save
	d.eventually(func() bool { _, err := os.Stat(filepath.Join(env.Root, "cfg", "config.toml")); return err == nil }, "config.toml")
	d.has("packages", "No packages yet")
	if !c.Cfg.Loaded {
		t.Error("core config should be marked loaded after setup")
	}

	// A second app now skips setup.
	cfg2, _ := config.Load()
	if !cfg2.Loaded {
		t.Error("saved config should load")
	}
}

// TestHomeConfigKeyReopensSetup covers re-running setup after first run: the
// fields start from the saved config, a save persists the change, and esc
// cancels back to Home without touching the config.
func TestHomeConfigKeyReopensSetup(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "")
	d.has("packages")

	d.key("c")
	d.has("config", "Source root", "Package root")
	d.key("esc")
	d.has("packages", "No packages yet")
	if len(d.app.env.core.Cfg.SourceRoots) != 0 {
		t.Errorf("esc should not change config, got %v", d.app.env.core.Cfg.SourceRoots)
	}

	d.key("c")
	d.typeText(env.Root)
	d.key("enter") // source root -> package root
	d.key("enter") // save
	d.has("packages", "config saved")
	if got := d.app.env.core.Cfg.SourceRoots; len(got) != 1 || got[0] != env.Root {
		t.Errorf("source roots = %v, want [%s]", got, env.Root)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SourceRoots) != 1 || cfg.SourceRoots[0] != env.Root {
		t.Errorf("saved source roots = %v, want [%s]", cfg.SourceRoots, env.Root)
	}

	// Reopening starts from the saved value.
	d.key("c")
	d.has(env.Root)
}

func TestHomeListsFiltersAndPreviews(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	seed(t, env, "payments", a)
	seed(t, env, "auth-rework", b)
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "auth-rework", "work", "beta", "wip.txt"), "x")

	d := newDriver(t, env, "")
	d.has("payments", "auth-rework", "1A", "SESSIONS")
	d.has("dirty", "clean") // auth-rework has untracked work

	d.key("/")
	d.typeText("pay")
	d.has("payments")
	d.lacks("auth-rework")
	d.key("esc")
	d.has("auth-rework")

	d.key("j")
	d.key("k")
	d.key("?")
	d.has("Copy path", "Command palette")
}

func TestOpenKeyRequestsLaunch(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "go", a)
	d := newDriver(t, env, "")
	d.key("o")
	if !d.quit || d.app.Launch() == nil || d.app.Launch().Package != "go" || d.app.Launch().Session != "work" {
		t.Fatalf("enter should request launch: quit=%v launch=%+v", d.quit, d.app.Launch())
	}
	if !strings.Contains(d.view(), "go") {
		t.Error("view should still render")
	}
}

func TestHomeMessageAndQuit(t *testing.T) {
	env := testutil.NewEnv(t)
	d := newDriver(t, env, "harness exploded")
	d.has("harness exploded")
	d.key("q")
	if !d.quit {
		t.Error("q should quit")
	}
}

// TestSetupTabCompletesSourceRoot covers directory completion in the
// source-root field, which lists candidates under the fields.
func TestSetupTabCompletesSourceRoot(t *testing.T) {
	env := testutil.NewEnv(t)
	env.NewSource(t, "alpha") // creates <root>/source and <root>/remotes
	d := newDriver(t, env, "")
	d.key("c")
	d.typeText(env.Root + string(os.PathSeparator) + "sou")
	d.key("tab")
	d.has(env.Root + string(os.PathSeparator) + "source" + string(os.PathSeparator))
	d.key("enter", "enter")
	d.has("config saved")
	got := d.app.env.core.Cfg.SourceRoots
	if len(got) != 1 || filepath.Base(got[0]) != "source" {
		t.Errorf("source roots = %v", got)
	}
}
