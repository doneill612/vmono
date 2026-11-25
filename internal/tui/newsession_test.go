package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestSessionPromptPrefillsEachMembersCheckedOutBranch(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	testutil.Git(t, b, "checkout", "--quiet", "-b", "develop")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.has("into alpha", "main", "into beta", "develop")
	d.typeText("stacked")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")

	data, err := os.ReadFile(filepath.Join(env.PackageRoot, "pay", "stacked", ".vmono", "session.toml"))
	if err != nil || !containsText(string(data), "alpha = \"main\"") || !containsText(string(data), "beta = \"develop\"") {
		t.Errorf("session.toml = %q, %v", data, err)
	}
}

func TestSessionPromptLandingBranchCanBeEditedPerMember(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	seed(t, env, "pay", a, b)
	testutil.Git(t, a, "branch", "feature")
	d := newDriver(t, env, "")

	d.key("right", "n")
	d.typeText("stacked")
	d.key("tab", "tab", "tab") // name -> branch -> start from -> alpha's into
	for range "main" {
		d.key("backspace")
	}
	d.typeText("feature")
	d.key("tab", "shift+tab") // to beta's field and back: the text stays
	d.has("feature")
	d.key("enter")
	d.eventually(func() bool { return containsView(d, "Launch now?") }, "launch prompt")

	data, _ := os.ReadFile(filepath.Join(env.PackageRoot, "pay", "stacked", ".vmono", "session.toml"))
	if !containsText(string(data), "alpha = \"feature\"") || !containsText(string(data), "beta = \"main\"") {
		t.Errorf("session.toml = %q", data)
	}
	want := testutil.Git(t, a, "rev-parse", "feature")
	if got := testutil.Git(t, filepath.Join(env.PackageRoot, "pay", "stacked", "alpha"), "rev-parse", "HEAD"); got != want {
		t.Errorf("alpha should start at feature: %s, want %s", got, want)
	}
}

// containsText reports whether s contains sub.
func containsText(s, sub string) bool { return indexOf(s, sub) >= 0 }
