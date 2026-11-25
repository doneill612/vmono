package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestEnterDrillsIntoSessionsThenMembersAndEscReturns(t *testing.T) {
	env := testutil.NewEnv(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	seed(t, env, "payments", a, b)
	testutil.WriteFile(t, filepath.Join(env.PackageRoot, "payments", "work", "beta", "wip.txt"), "x")

	d := newDriver(t, env, "")
	d.key("right")
	d.has("Sessions · payments (1)", "work", "packages › payments")
	d.key("m")
	d.has("Members · payments/work (2)", "alpha", "beta", "active", "role", "packages › payments › work")
	d.has("untr")
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
	d.has("Sessions · payments (1)")
	d.lacks("Members · payments/work")
	d.key("esc")
	d.has("Packages (1)")
	d.lacks("Sessions · payments")
}

func TestSessionsAndMembersOpenLaunchTheSession(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("right", "o")
	if l := d.app.Launch(); !d.quit || l == nil || l.Package != "payments" || l.Session != "work" {
		t.Fatalf("launch from sessions = %+v", l)
	}

	d = newDriver(t, env, "")
	d.key("right", "m", "o")
	if l := d.app.Launch(); !d.quit || l == nil || l.Package != "payments" || l.Session != "work" {
		t.Fatalf("launch from members = %+v", l)
	}
}

func TestDescribePackageAndMember(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("d")
	d.has("Describe · payments", "PURPOSE", "Purpose of payments", "CONTEXT", "NOTES", "SESSIONS", "work", "MEMBERS", "alpha")
	d.lacks("Branch:") // a package alone has no branch; its sessions do
	d.key("esc")
	d.has("Packages (1)")

	d.key("right", "d")
	d.has("Describe · payments/work", "Session", "Branch", "MEMBERS", "alpha")
	d.key("esc")
	d.has("Sessions · payments")

	d.key("m", "d")
	d.has("Describe · payments/work/alpha", "LOCATION", "Source repo", "STATE", "Branch/ref", "0 modified")
	d.key("esc")
	d.has("Members · payments/work")
}

func TestDescribeEditOpensEditor(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.key("d", "e")
	d.has("Edit · payments")
	d.key("esc")
	d.has("Describe · payments")
}

func TestSessionsOfAPackageFromThePalette(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	seed(t, env, "auth", env.NewSource(t, "beta"))
	d := newDriver(t, env, "")

	d.key(":")
	d.typeText("sessions of pay")
	d.key("enter")
	d.has("Sessions · payments (1)", "packages › payments")
	d.key("esc")
	d.has("Packages (2)")
}

func TestMembersFromASessionsPalette(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("right", ":")
	d.typeText("members")
	d.key("enter")
	d.has("Members · payments/work")
}
