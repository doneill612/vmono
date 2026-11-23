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
	d.has("sessions(payments)[1]", "work", "vmono/payments", "<payments>")
	d.key("m")
	d.has("members(payments/work)[2]", "alpha", "beta", "active", "role", "<work>")
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
	d.has("sessions(payments)[1]")
	d.lacks("members(payments/work)")
	d.key("esc")
	d.has("packages[1]")
	d.lacks("sessions(payments)")
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
	d.has("describe(payments)", "PURPOSE", "Purpose of payments", "CONTEXT", "NOTES", "SESSIONS", "work", "MEMBERS", "alpha")
	d.lacks("Branch:") // a package alone has no branch; its sessions do
	d.key("esc")
	d.has("packages[1]")

	d.key("right", "d")
	d.has("describe(payments/work)", "Session", "Branch", "vmono/payments", "MEMBERS", "alpha")
	d.key("esc")
	d.has("sessions(payments)")

	d.key("m", "d")
	d.has("describe(payments/work/alpha)", "LOCATION", "Source repo", "STATE", "Branch/ref", "0 modified")
	d.key("esc")
	d.has("members(payments/work)")
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
	d.has("no package")

	d.key(":")
	d.typeText("members pay")
	d.key("tab") // completes the package name
	d.key("enter")
	d.has("members(payments/work)", "<packages>")

	d.key("esc")
	d.key(":")
	d.typeText("mem")
	d.key("enter") // no argument: the selected package, which has one session
	d.has("members(")
}

func TestSessionsCommand(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "payments", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key(":")
	d.typeText("sessions nope")
	d.key("enter")
	d.has("no such package: nope")

	d.key(":")
	d.typeText("sessions pay")
	d.key("tab")
	d.key("enter")
	d.has("sessions(payments)[1]", "<packages>")
}
