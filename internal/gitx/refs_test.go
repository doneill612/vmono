package gitx

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// TestSnapshotAnswersLikeLiveRefs compares the two kinds of Refs over the
// shapes a source repo comes in.
func TestSnapshotAnswersLikeLiveRefs(t *testing.T) {
	env := testutil.NewEnv(t)
	git := func(dir string, args ...string) { testutil.Git(t, dir, args...) }
	repo := func(name string, change func(src string)) string {
		src := env.NewSource(t, name)
		change(src)
		return src
	}
	remoteOf := func(name string) string { return filepath.Join(env.Root, "remotes", name+".git") }
	repos := map[string]string{
		"plain": repo("plain", func(string) {}),
		"several branches": repo("several", func(s string) {
			git(s, "branch", "feat/x")
			git(s, "push", "--quiet", "origin", "feat/x")
		}),
		"remote only": repo("remoteonly", func(s string) {
			git(s, "branch", "feat/x")
			git(s, "push", "--quiet", "origin", "feat/x")
			git(s, "branch", "-D", "feat/x")
		}),
		"other upstream": repo("other", func(s string) {
			git(s, "push", "--quiet", "origin", "main:elsewhere")
			git(s, "branch", "--set-upstream-to=origin/elsewhere", "main")
		}),
		"deleted upstream": repo("deleted", func(s string) { git(s, "update-ref", "-d", "refs/remotes/origin/main") }),
		"local upstream": repo("local", func(s string) {
			git(s, "branch", "feat/x")
			git(s, "branch", "--set-upstream-to=main", "feat/x")
		}),
		"no remote": repo("noremote", func(s string) { git(s, "remote", "remove", "origin") }),
		"several remotes": repo("tworemotes", func(s string) {
			git(s, "remote", "add", "fork", remoteOf("tworemotes"))
			git(s, "fetch", "--quiet", "fork")
		}),
		"remote with a slash": repo("slash", func(s string) {
			git(s, "remote", "add", "team/fork", remoteOf("slash"))
			git(s, "fetch", "--quiet", "team/fork")
			git(s, "branch", "--set-upstream-to=team/fork/main", "main")
		}),
		"tag": repo("tag", func(s string) { git(s, "tag", "v1") }),
	}
	refs := []string{
		"refs/heads/main", "refs/heads/feat/x", "v1",
		"refs/remotes/origin/main", "refs/remotes/origin/feat/x", "refs/remotes/origin/HEAD", "refs/remotes/origin/elsewhere",
		"refs/remotes/fork/main", "refs/remotes/team/fork/main", "origin/HEAD", "origin/main",
	}
	for name, src := range repos {
		live := LiveRefs(src)
		exists := map[string]bool{}
		for _, ref := range refs {
			exists[ref] = live.Exists(ref)
		}
		wantRef, wantFell := live.ResolveDefault("")
		for _, branch := range []string{"main", "feat/x"} {
			snap := SnapshotRefs(src, branch)
			for _, ref := range refs {
				if got := snap.Exists(ref); got != exists[ref] {
					t.Errorf("%s: snapshot of %q says %s exists: %v, git says %v", name, branch, ref, got, exists[ref])
				}
			}
			if ref, fell := snap.ResolveDefault(""); ref != wantRef || fell != wantFell {
				t.Errorf("%s: snapshot of %q resolves the default to %s %v, git to %s %v", name, branch, ref, fell, wantRef, wantFell)
			}
			if l, s := live.BranchExists(branch), snap.BranchExists(branch); l != s {
				t.Errorf("%s: snapshot says branch %s exists: %v, git says %v", name, branch, s, l)
			}
			lt, lok := live.UpstreamOf(branch)
			if st, sok := snap.UpstreamOf(branch); st != lt || sok != lok {
				t.Errorf("%s: snapshot has %s tracking %+v %v, git %+v %v", name, branch, st, sok, lt, lok)
			}
		}
	}
}

func TestAheadBehind(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	testutil.Git(t, src, "branch", "other")
	commitFile(t, src, "a", "a", "one")
	commitFile(t, src, "b", "b", "two")
	testutil.Git(t, src, "checkout", "--quiet", "other")
	commitFile(t, src, "c", "c", "three")
	if ahead, behind, err := AheadBehind(src, "main", "other"); err != nil || ahead != 2 || behind != 1 {
		t.Errorf("AheadBehind = %d, %d, %v; want 2, 1", ahead, behind, err)
	}
	if _, _, err := AheadBehind(src, "main", "missing"); err == nil {
		t.Error("a missing ref should be an error")
	}
}

func TestWorkTreeOfABareRepoIsAnError(t *testing.T) {
	env := testutil.NewEnv(t)
	src := env.NewSource(t, "alpha")
	if top, bare, err := WorkTree(src); err != nil || bare || filepath.Base(top) != "alpha" {
		t.Errorf("WorkTree = %q, %v, %v", top, bare, err)
	}
	if _, _, err := WorkTree(filepath.Join(env.Root, "remotes", "alpha.git")); err == nil {
		t.Error("a bare repo has no working tree")
	}
}
