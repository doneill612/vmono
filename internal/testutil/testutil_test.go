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

// TestNewSourceCopiesAreIndependent checks that a copied source is the same as
// one built from scratch, and that its remote is its own.
func TestNewSourceCopiesAreIndependent(t *testing.T) {
	var urls, heads []string
	for range 2 {
		t.Run("env", func(t *testing.T) {
			env := testutil.NewEnv(t)
			src := env.NewSource(t, "alpha")
			if got := testutil.Git(t, src, "status", "--porcelain"); got != "" {
				t.Errorf("a new source is dirty: %s", got)
			}
			if got := testutil.Git(t, src, "rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
				t.Errorf("main tracks %s", got)
			}
			if got := testutil.Git(t, src, "symbolic-ref", "refs/remotes/origin/HEAD"); got != "refs/remotes/origin/main" {
				t.Errorf("origin/HEAD is %s", got)
			}
			url := testutil.Git(t, src, "remote", "get-url", "origin")
			if want := filepath.Join(env.Root, "remotes", "alpha.git"); filepath.Clean(url) != want {
				t.Errorf("remote is %s, want %s", url, want)
			}
			testutil.WriteFile(t, filepath.Join(src, "new.txt"), "x")
			testutil.Git(t, src, "add", "-A")
			testutil.Git(t, src, "commit", "--quiet", "-m", "second")
			testutil.Git(t, src, "push", "--quiet")
			if got := testutil.Git(t, src, "rev-list", "--count", "origin/main"); got != "2" {
				t.Errorf("origin/main has %s commits, want 2", got)
			}
			urls = append(urls, url)
			heads = append(heads, testutil.Git(t, src, "rev-parse", "main~"))
		})
	}
	if len(urls) == 2 && urls[0] == urls[1] {
		t.Error("two sources share a remote")
	}
	if len(heads) == 2 && heads[0] != heads[1] {
		t.Error("the second source was not copied from the first")
	}
}

// TestMain runs the tests here with source templates, like the packages that
// use this one.
func TestMain(m *testing.M) {
	os.Exit(testutil.Main(m))
}
