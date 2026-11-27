package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestRemotePickerWorksOffline(t *testing.T) {
	env := testutil.NewEnv(t)
	useFakeHarness(t, env)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:feat/x")
	testutil.Git(t, a, "fetch", "--quiet")
	seedPackage(t, env, "pay", a)
	testutil.Git(t, a, "remote", "set-url", "origin", filepath.Join(env.Root, "gone.git"))
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("work")
	d.key("tab", "tab", "space") // the build on field opens the picker
	d.eventually(func() bool { return containsView(d, "couldn't fetch alpha") }, "the fetch notice")
	d.has("feat/x")
	d.typeText("feat")
	d.key("enter")
	d.has("feat/x  (alpha)")

	// Picking none restores the default.
	d.key("space", "enter")
	d.has("each member's base branch")
	d.lacks("feat/x")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")
}
