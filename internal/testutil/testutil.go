// Package testutil builds throwaway git fixtures for integration tests: a
// bare remote and source clones in a temp directory, with git identity and
// vmono config/state isolated from the real machine.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env is an isolated test environment.
type Env struct {
	// Root is the temp directory everything lives under.
	Root string
	// Home is the fake home directory.
	Home string
	// PackageRoot is where packages are created.
	PackageRoot string
}

// NewEnv isolates git and vmono from the real machine: a fake HOME, no
// global or system git config, a fixed identity, temp config/state dirs, and
// a working directory inside the temp root.
//
// The working directory matters because vmono infers the package and session
// by walking up from it. `go test` starts in the package under test, and
// when the tests are run from a vmono session that directory sits inside a
// member worktree of the real session, so any command that infers its target
// (note, describe, sync, ...) would find and modify the developer's actual
// package instead of failing with "not inside a package". Moving into the
// temp root first makes inference see only the fixtures. Tests that need a
// different directory change it themselves afterwards.
func NewEnv(t *testing.T) *Env {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	home := filepath.Join(root, "home")
	mustMkdir(t, home)
	gitconfig := filepath.Join(root, "gitconfig")
	write(t, gitconfig, "[user]\n\tname = Test User\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n[protocol \"file\"]\n\tallow = always\n[gc]\n\tauto = 0\n[maintenance]\n\tauto = false\n[receive]\n\tautogc = false\n[core]\n\tfsync = none\n")

	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("VMONO_CONFIG_DIR", filepath.Join(root, "cfg"))
	t.Setenv("VMONO_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	for _, k := range []string{"VMONO_PACKAGE", "VMONO_ROOT", "VMONO_AUTHOR", "VMONO_PACKAGE_ROOT", "VMONO_SOURCE_ROOTS", "VMONO_AUTO_COMMIT", "VMONO_FETCH_ON_CREATE", "VMONO_RETURN_TO_TUI", "VMONO_DEFAULT_HARNESS", "VMONO_SCAN_DEPTH", "VMONO_LAUNCHER", "VMONO_TMUX_SWITCH", "VMONO_NO_HOOKS", "VMONO_HERDR_FOCUS", "HERDR_ENV"} {
		t.Setenv(k, "")
	}
	pk := filepath.Join(root, "pkgs")
	mustMkdir(t, pk)
	t.Setenv("VMONO_PACKAGE_ROOT", pk)
	return &Env{Root: root, Home: home, PackageRoot: pk}
}

// Git runs git in dir and fails the test on error, returning stdout.
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// templateRoot is where NewSource keeps the repos it copies, one directory per
// source name. Main sets it for the life of the process; while it is empty,
// every source is built from scratch.
var templateRoot string

// NewSource creates a bare remote named <name>.git and a clone at
// <Root>/source/<name> with one commit on main pushed to it, and returns the
// clone path. Tracked files: README.md.
//
// Building the pair takes nine git commands, which are slow to start on some
// systems, so under Main each name is built once per process and copied after
// that. Only the remote's URL in the clone's config depends on where the pair
// lives, and it is rewritten in the copy. Sources of one name therefore share
// their initial commit id across tests, which run in separate directories.
func (e *Env) NewSource(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join(e.Root, "source", name)
	remote := filepath.Join(e.Root, "remotes", name+".git")
	if templateRoot == "" {
		buildSource(t, name, remote, src)
		return src
	}
	tmpl := filepath.Join(templateRoot, name)
	tmplRemote, tmplSrc := filepath.Join(tmpl, "remote.git"), filepath.Join(tmpl, "source")
	if _, err := os.Stat(filepath.Join(tmpl, "done")); err != nil {
		buildSource(t, name, tmplRemote, tmplSrc)
		write(t, filepath.Join(tmpl, "done"), "")
	}
	copyDir(t, tmplRemote, remote)
	copyDir(t, tmplSrc, src)
	Git(t, src, "remote", "set-url", "origin", remote)
	return src
}

// buildSource makes the remote and clone that NewSource describes.
func buildSource(t *testing.T, name, remote, src string) {
	t.Helper()
	mustMkdir(t, remote)
	Git(t, remote, "init", "--bare", "--quiet", "--initial-branch=main")

	mustMkdir(t, src)
	Git(t, src, "init", "--quiet", "--initial-branch=main")
	write(t, filepath.Join(src, "README.md"), "# "+name+"\n")
	Git(t, src, "add", "-A")
	Git(t, src, "commit", "--quiet", "-m", "initial")
	Git(t, src, "remote", "add", "origin", remote)
	Git(t, src, "push", "--quiet", "-u", "origin", "main")
	Git(t, src, "remote", "set-head", "origin", "main")
}

// copyDir copies the tree at from to the new directory to. os.CopyFS keeps the
// executable bits, which git hooks need, and makes files writable.
func copyDir(t *testing.T, from, to string) {
	t.Helper()
	if err := os.CopyFS(to, os.DirFS(from)); err != nil {
		t.Fatal(err)
	}
}

// WriteFile writes content to path, creating parent directories.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	write(t, path, content)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
