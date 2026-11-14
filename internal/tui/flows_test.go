package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// useFakeHarness writes a config whose default harness is "sh" (present on
// every unix CI image) alongside one that is certainly not installed.
func useFakeHarness(t *testing.T, env *testutil.Env) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"), `
default_harness = "fake"

[harness.fake]
command = "sh"

[harness.missing]
command = "definitely-not-installed-xyz"
`)
}

func TestDeleteCleanPackageWithY(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "gone", a)
	d := newDriver(t, env, "")

	d.key("d")
	d.has("Delete package", "alpha", "clean", "Everything is clean")
	d.key("n")
	d.has("packages")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "gone")); err != nil {
		t.Fatal("cancel must not delete")
	}

	d.key("d")
	d.key("y")
	d.eventually(func() bool { _, err := os.Stat(filepath.Join(env.PackageRoot, "gone")); return os.IsNotExist(err) }, "package removal")
	d.has("done")
	d.key("enter")
	d.has("No packages yet")
}

func TestDeleteDirtyRequiresTypedName(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "risky", a)
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "risky", "alpha", "wip.txt"), "x")
	d := newDriver(t, env, "")

	d.key("d")
	d.has("untracked", "Type the package name")
	d.key("y") // 'y' is just text here, not a confirmation
	d.key("enter")
	d.has("type the package name exactly")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "risky")); err != nil {
		t.Fatal("wrong confirmation must not delete")
	}
	d.key("backspace")
	d.typeText("risky")
	d.key("enter")
	d.eventually(func() bool { _, err := os.Stat(filepath.Join(env.PackageRoot, "risky")); return os.IsNotExist(err) }, "forced delete")
}

func TestSyncEndsWithLaunchNow(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "heal", a)
	if err := os.RemoveAll(filepath.Join(env.PackageRoot, "heal", "alpha")); err != nil {
		t.Fatal(err)
	}
	d := newDriver(t, env, "")

	d.key("s")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
	d.has("fake", "default")
	if _, err := os.Stat(filepath.Join(env.PackageRoot, "heal", "alpha", "README.md")); err != nil {
		t.Error("sync should have recreated the worktree")
	}
	d.key("enter")
	if !d.quit || d.app.Launch() == nil || d.app.Launch().Package != "heal" || d.app.Launch().Harness != "fake" {
		t.Fatalf("launch = %+v quit=%v", d.app.Launch(), d.quit)
	}
}

func TestHarnessPickerBlocksMissingCommand(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pick", a)
	d := newDriver(t, env, "")

	d.key("l")
	d.has("Launch with", "fake", "missing", "not found on PATH")
	d.key("down") // fake -> missing (sorted: fake, missing)
	d.key("enter")
	d.has("is not on PATH")
	if d.quit {
		t.Fatal("a missing harness must not launch")
	}
	d.key("up")
	d.key("enter")
	if d.app.Launch() == nil || d.app.Launch().Harness != "fake" {
		t.Fatalf("launch = %+v", d.app.Launch())
	}
}

func TestPickerEscReturnsHome(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	seed(t, env, "back", a)
	d := newDriver(t, env, "")
	d.key("l")
	d.key("esc")
	d.has("packages")
	d.lacks("Launch with")
}

// containsView reports whether the current view contains s.
func containsView(d *driver, s string) bool {
	return len(s) > 0 && indexOf(d.view(), s) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
