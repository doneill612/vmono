package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// sep is the separator the finder prints after directory names.
var sep = string(os.PathSeparator)

// initRepo makes dir a git repo with one file.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, "f.txt"), "x")
	testutil.Git(t, dir, "init", "--quiet", "--initial-branch=main")
}

// finderTree builds ~/work with repos api and web, a repo nested at
// org/lib, and a plain directory notes, and returns ~/work's real path.
func finderTree(t *testing.T, env *testutil.Env) string {
	t.Helper()
	work := filepath.Join(env.Home, "work")
	initRepo(t, filepath.Join(work, "api"))
	initRepo(t, filepath.Join(work, "web"))
	initRepo(t, filepath.Join(work, "org", "lib"))
	testutil.WriteFile(t, filepath.Join(work, "notes", "todo.txt"), "x")
	return work
}

// openFinder opens the finder from the repos picker with an empty prompt.
func openFinder(t *testing.T, d *driver) {
	t.Helper()
	d.key("n")
	d.typeText("x")
	d.key("ctrl+s", "p")
	d.has("find repo")
	d.key("ctrl+u")
}

func TestFinderListsDirectoriesReposAndNestedRepos(t *testing.T) {
	env := testutil.NewEnv(t)
	finderTree(t, env)
	d := newDriver(t, env, "")
	openFinder(t, d)

	d.typeText("~/work/")
	d.has("api", "web", "notes"+sep, "org"+sep)
	d.has("●")                        // repos are marked
	d.has("org"+sep+"lib", "(below)") // found by scanning beneath the typed directory
}

func TestFinderNarrowsByPrefixAndFuzzy(t *testing.T) {
	env := testutil.NewEnv(t)
	finderTree(t, env)
	d := newDriver(t, env, "")
	openFinder(t, d)

	d.typeText("~/work/ap")
	d.has("api")
	d.lacks("web", "notes")

	d.key("ctrl+u")
	d.typeText("~/work/wb") // not a prefix of anything, but fuzzy-matches web
	d.has("web")
	d.lacks("api", "notes")
}

func TestFinderTabStepsIntoDirectoriesAndEnterPicksRepos(t *testing.T) {
	env := testutil.NewEnv(t)
	finderTree(t, env)
	d := newDriver(t, env, "")
	openFinder(t, d)

	d.typeText("~/work/org")
	d.key("tab") // org is a plain directory: tab steps into it
	d.has("~/work/org"+sep, "lib")
	d.key("enter") // lib is highlighted and is a repo
	d.has("Choose repos", "added lib", "[x]")
}

func TestFinderEnterOnDirectoryDescends(t *testing.T) {
	env := testutil.NewEnv(t)
	finderTree(t, env)
	d := newDriver(t, env, "")
	openFinder(t, d)
	d.typeText("~/work/not")
	d.key("enter")
	d.has("find repo", "~/work/notes"+sep) // stepped in rather than picking
}

func TestFinderWorksWithNoSourceRootAndRemembersRepo(t *testing.T) {
	env := testutil.NewEnv(t)
	work := finderTree(t, env)
	d := newDriver(t, env, "")
	if len(d.app.env.core.Cfg.SourceRoots) != 0 {
		t.Fatal("test needs no source root")
	}
	d.key("n")
	d.typeText("fresh")
	d.key("ctrl+s")
	d.has("No repos found", "no source root is configured")
	d.key("p")
	d.has("~/") // starts at the home directory
	d.key("ctrl+u")
	d.typeText(work + string(os.PathSeparator) + "ap")
	d.key("enter")
	d.has("added api")

	// Remembered: it is listed the next time without searching for it.
	d.key("esc") // picker -> editor
	d.key("esc")
	d.key("n")
	d.typeText("again")
	d.key("ctrl+s")
	d.eventually(func() bool { return containsView(d, "api") }, "remembered repo")
}

func TestFinderStartsInFirstSourceRoot(t *testing.T) {
	env := testutil.NewEnv(t)
	a := env.NewSource(t, "alpha")
	t.Setenv("VMONO_SOURCE_ROOTS", filepath.Dir(a))
	d := newDriver(t, env, "")
	d.key("n")
	d.typeText("x")
	d.key("ctrl+s", "p")
	d.has("find repo", "alpha") // listed without typing anything
}
