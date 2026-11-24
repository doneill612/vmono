package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/testutil"
)

// burst delivers several keys back to back and only then runs the commands
// they returned. It reproduces what Bubble Tea does when keys are already
// queued (a held arrow key, a fast double press, two keys parsed from one
// terminal read): every key reaches the screen that was on top when the
// burst began, because the navigation messages its commands produce are
// queued behind the keys.
func (d *driver) burst(names ...string) {
	d.t.Helper()
	var cmds []tea.Cmd
	for _, n := range names {
		_, cmd := d.app.Update(keyMsg(n))
		cmds = append(cmds, cmd)
	}
	for _, c := range cmds {
		d.run(c)
	}
}

// crumbs returns the breadcrumb labels of the screen stack, skipping
// dialogs, which add none.
func (d *driver) crumbs() string {
	var out []string
	for _, s := range d.app.stack {
		if c := s.Crumb(); c != "" {
			out = append(out, c)
		}
	}
	return strings.Join(out, " > ")
}

// TestRepeatedDrillDownKeyOpensOneView guards against the stack nesting the
// same view several times ("packages > pay > pay > pay") when the drill-down
// key arrives again before the first push has been applied.
func TestRepeatedDrillDownKeyOpensOneView(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.burst("right", "right", "right")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("crumbs = %q, want %q", got, "packages > pay")
	}
	d.key("esc")
	if got := d.crumbs(); got != "packages" {
		t.Fatalf("after one esc crumbs = %q, want %q", got, "packages")
	}
}

// TestRepeatedMenuChoiceActsOnce guards the action menu: a second Enter that
// reaches it before its pop is applied must not perform the action again.
func TestRepeatedMenuChoiceActsOnce(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("enter") // open the package menu; "Sessions" is highlighted
	d.burst("enter", "enter")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("crumbs = %q, want %q", got, "packages > pay")
	}
	if _, ok := d.app.top().(*sessionsModel); !ok {
		t.Fatalf("top = %T, want *sessionsModel", d.app.top())
	}
}

// TestRepeatedBackKeyClosesOneView guards the reverse: an esc queued behind
// another esc was meant for the view that has just closed, so it must not
// close the view revealed beneath it as well.
func TestRepeatedBackKeyClosesOneView(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")

	d.key("right", "m") // packages > pay > work (members)
	if got := d.crumbs(); got != "packages > pay > work" {
		t.Fatalf("crumbs = %q", got)
	}
	d.burst("esc", "esc")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("crumbs = %q, want %q", got, "packages > pay")
	}
}

// TestFinishThenPushReturnsToOneSessionsView guards against an operation's
// progress screen rebuilding the view it came from on top of the original,
// which nested the sessions view once more after every finish or push
// ("packages > pay > pay > pay").
func TestFinishThenPushReturnsToOneSessionsView(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	d := newDriver(t, env, "")

	d.key("right", "F", "y")
	d.eventually(func() bool { return containsView(d, "done") }, "finish to complete")
	d.key("enter")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("after finish crumbs = %q, want %q", got, "packages > pay")
	}

	d.key("P")
	d.has("push session")
	d.key("y")
	d.eventually(func() bool { return containsView(d, "done") }, "push to complete")
	d.key("enter")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("after push crumbs = %q, want %q", got, "packages > pay")
	}
	d.key("esc")
	if got := d.crumbs(); got != "packages" {
		t.Fatalf("after one esc crumbs = %q, want %q", got, "packages")
	}
}

// TestFinishAndRemoveReturnsToOneSessionsView covers finishing with the
// session removed afterward: the sessions view must come back once, empty.
func TestFinishAndRemoveReturnsToOneSessionsView(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	seed(t, env, "pay", a)
	commitInSession(t, env, "pay", "work", "alpha", "one.txt")
	d := newDriver(t, env, "")

	d.key("right", "F", "c", "y")
	d.eventually(func() bool { return containsView(d, "done") }, "finish to complete")
	d.key("enter")
	if got := d.crumbs(); got != "packages > pay" {
		t.Fatalf("crumbs = %q, want %q", got, "packages > pay")
	}
	d.eventually(func() bool { return containsView(d, "No sessions yet") }, "the list to reload empty")
}

// TestPushFromMembersReturnsToMembers guards that a push started from the
// members view returns to that view, not to a second copy of it.
func TestPushFromMembersReturnsToMembers(t *testing.T) {
	env := testutil.NewEnv(t)
	finishedPay(t, env, "alpha")
	d := newDriver(t, env, "")

	d.key("right", "m", "p", "y")
	d.eventually(func() bool { return containsView(d, "done") }, "push to complete")
	d.key("enter")
	if got := d.crumbs(); got != "packages > pay > work" {
		t.Fatalf("crumbs = %q, want %q", got, "packages > pay > work")
	}
	if _, ok := d.app.top().(*membersModel); !ok {
		t.Fatalf("top = %T, want *membersModel", d.app.top())
	}
}
