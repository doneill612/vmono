package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestEnterDrillsIntoMembersAndEscReturns(t *testing.T) {
	env := testutil.NewEnv(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "payments", a, b)
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "payments", "beta", "wip.txt"), "x")

	d := newDriver(t, env, "")
	d.key("enter")
	d.has("members(payments)[2]", "alpha", "beta", "active", "role", "<payments>")
	d.has("UNTR")
	if r, _ := d.app.top().(*membersModel).tb.selected(); r.id != "alpha" {
		t.Fatalf("selected %q", r.id)
	}
	d.key("j")
	m := d.app.top().(*membersModel)
	ms, _ := m.selectedMember()
	if ms.Name != "beta" || ms.Untracked != 1 {
		t.Errorf("beta should show one untracked file: %+v", ms)
	}

	d.key("esc")
	d.has("packages[1]")
	d.lacks("members(payments)")
}

func TestMembersOpenLaunchesPackage(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("enter", "o")
	if !d.quit || d.app.Launch() == nil || d.app.Launch().Package != "payments" {
		t.Fatalf("launch = %+v", d.app.Launch())
	}
}

func TestDescribePackageAndMember(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("d")
	d.has("describe(payments)", "PURPOSE", "Purpose of payments", "CONTEXT", "NOTES", "MEMBERS", "alpha", "vmono/payments")
	d.key("esc")
	d.has("packages[1]")

	d.key("enter", "d")
	d.has("describe(payments/alpha)", "LOCATION", "Source repo", "STATE", "Branch/ref", "0 modified")
	d.key("esc")
	d.has("members(payments)")
}

func TestDescribeEditOpensEditor(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("d", "e")
	d.has("edit(payments)")
	d.key("esc")
	d.has("describe(payments)")
}

func TestMembersCommand(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	seed(t, env, "auth", env.NewSource(t, "beta"))
	d := newDriver(t, env, "")

	d.key(":")
	d.typeText("members nope")
	d.key("enter")
	d.has("no such package: nope")

	d.key(":")
	d.typeText("members pay")
	d.key("tab") // completes the package name
	d.key("enter")
	d.has("members(payments)", "<packages>")

	d.key("esc")
	d.key(":")
	d.typeText("mem")
	d.key("enter") // no argument: the selected package
	d.has("members(")
}
