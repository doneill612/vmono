package testutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// TestNewEnvLeavesAnEnclosingSession simulates `go test` being run from a
// member worktree of a real vmono session: the starting directory has a
// package manifest and a session file above it. NewEnv must move the test
// out of there, or commands that infer their package from the working
// directory would act on the developer's real package.
func TestNewEnvLeavesAnEnclosingSession(t *testing.T) {
	pkg := t.TempDir()
	session := filepath.Join(pkg, "work")
	member := filepath.Join(session, "repo", "internal", "cli")
	testutil.WriteFile(t, filepath.Join(pkg, ".vmono", "manifest.toml"), "")
	testutil.WriteFile(t, filepath.Join(session, ".vmono", "session.toml"), "")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(member)
	t.Setenv("VMONO_ROOT", session)

	testutil.NewEnv(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Find(wd); err == nil {
		t.Errorf("a package is still found from %s", wd)
	}
	if _, err := manifest.FindSession(wd); err == nil {
		t.Errorf("a session is still found from %s", wd)
	}
}
