package core

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// splitBranch gives src a local branch and a remote copy of it that each have a
// commit the other lacks.
func splitBranch(t *testing.T, src, branch string) {
	t.Helper()
	testutil.Git(t, src, "push", "--quiet", "origin", "main:"+branch)
	testutil.Git(t, src, "fetch", "--quiet")
	testutil.Git(t, src, "checkout", "--quiet", "-B", branch, "--track", "origin/"+branch)
	commitIn(t, src, "local-"+filepath.Base(branch)+".txt")
	testutil.Git(t, src, "checkout", "--quiet", "--detach")
	pushFromElsewhere(t, src, branch, "remote-"+filepath.Base(branch)+".txt", "r\n")
}

func TestAFullRefIsBuiltOnAndFollowedExactly(t *testing.T) {
	for name, tc := range map[string]struct {
		from, follows, shown string
	}{
		"local":  {"refs/heads/main", "refs/heads/main", "main"},
		"remote": {"refs/remotes/origin/main", "refs/remotes/origin/main", "origin/main"},
		// A plain name takes the local branch once the two have diverged.
		"plain": {"main", "refs/heads/main", "main"},
	} {
		t.Run(name, func(t *testing.T) {
			c, env := newCore(t)
			a := env.NewSource(t, "alpha")
			commitIn(t, a, "local-only.txt")
			pushFromElsewhere(t, a, "main", "remote-only.txt", "r\n")
			p := mustPackage(t, c, request("exact", []string{a}, nil))

			s, err := c.CreateSession(p, "work", SessionOptions{From: tc.from})
			if err != nil {
				t.Fatal(err)
			}
			wt := filepath.Join(s.Dir, "alpha")
			if sha(t, wt, "HEAD") != sha(t, a, tc.follows) {
				t.Errorf("starts somewhere other than %s", tc.follows)
			}
			if _, m := status(t, c, s, "alpha"); m.BuiltOn != tc.shown {
				t.Errorf("built on %q, want %q", m.BuiltOn, tc.shown)
			}

			// The next commit on the side it follows is the one it takes in.
			if tc.follows == "refs/heads/main" {
				commitIn(t, a, "later.txt")
			} else {
				pushFromElsewhere(t, a, "main", "later.txt", "l\n")
			}
			if _, err := c.Update(s, UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			if sha(t, wt, "HEAD") != sha(t, a, tc.follows) {
				t.Errorf("update did not follow %s", tc.follows)
			}
			if got := s.S.Members["alpha"].From; got != tc.from {
				t.Errorf("from = %q, want it left as %q", got, tc.from)
			}
		})
	}
}

// TestALocalParentBehindItsRemoteIsNotTakenForMerged covers a local branch that
// holds nothing the base lacks while the published branch still does.
func TestALocalParentBehindItsRemoteIsNotTakenForMerged(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "push", "--quiet", "origin", "main:parent")
	testutil.Git(t, a, "fetch", "--quiet")
	testutil.Git(t, a, "branch", "--track", "parent", "origin/parent")
	pushFromElsewhere(t, a, "parent", "theirs.txt", "t\n")
	p := mustPackage(t, c, request("behind", []string{a}, nil))
	s, err := c.CreateSession(p, "child", SessionOptions{From: "refs/heads/parent"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "child.txt")

	res, err := c.Update(s, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.S.Members["alpha"].From; got != "refs/heads/parent" || len(res.Members[0].Notes) != 0 {
		t.Errorf("from = %q, notes %v: the parent has not merged", got, res.Members[0].Notes)
	}

	// Once the published parent has merged, the session moves to its base.
	mergeOnRemote(t, a, "parent", MergedCommit, true)
	res, err = c.Update(s, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.S.Members["alpha"].From; got != "" {
		t.Errorf("from = %q, notes %v: want the base", got, res.Members[0].Notes)
	}
}

func TestAMergedRemoteParentPickedExactlyMovesToTheBase(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	pushFromElsewhere(t, a, "parent", "theirs.txt", "t\n")
	p := mustPackage(t, c, request("remote", []string{a}, nil))
	s, err := c.CreateSession(p, "child", SessionOptions{From: "refs/remotes/origin/parent"})
	if err != nil {
		t.Fatal(err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "child.txt")
	mergeOnRemote(t, a, "parent", MergedSquash, true)

	res, err := c.Update(s, UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.S.Members["alpha"].From; got != "" {
		t.Errorf("from = %q, notes %v: want the base", got, res.Members[0].Notes)
	}
}

func TestAReferenceCheckedOutAtAFullRefFollowsIt(t *testing.T) {
	for name, ref := range map[string]string{"local": "refs/heads/feat", "remote": "refs/remotes/origin/feat"} {
		t.Run(name, func(t *testing.T) {
			c, env := newCore(t)
			a, lib := env.NewSource(t, "alpha"), env.NewSource(t, "lib")
			splitBranch(t, lib, "feat")
			p := mustPackage(t, c, request("ref", []string{a}, []string{lib}))
			s, err := c.CreateSession(p, "work", SessionOptions{FromByMember: map[string]string{"lib": ref}})
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(s.Dir, "_ref", "lib")
			if sha(t, dir, "HEAD") != sha(t, lib, ref) {
				t.Errorf("checked out somewhere other than %s", ref)
			}
			if res, err := c.Sync(s, SyncOptions{Refs: true}); err != nil || sha(t, dir, "HEAD") != sha(t, lib, ref) {
				t.Errorf("sync --refs moved it off %s: %+v, %v", ref, res, err)
			}
		})
	}
}

// TestAReferenceAtALocalBranchReturnsOnceItsRemoteCopyMerges covers sync --refs
// noticing a deleted remote copy for a local branch named in full.
func TestAReferenceAtALocalBranchReturnsOnceItsRemoteCopyMerges(t *testing.T) {
	c, env := newCore(t)
	a, lib := env.NewSource(t, "alpha"), env.NewSource(t, "lib")
	pushFromElsewhere(t, lib, "feat", "theirs.txt", "t\n")
	testutil.Git(t, lib, "fetch", "--quiet")
	testutil.Git(t, lib, "branch", "--track", "feat", "origin/feat")
	p := mustPackage(t, c, request("gone", []string{a}, []string{lib}))
	s, err := c.CreateSession(p, "work", SessionOptions{FromByMember: map[string]string{"lib": "refs/heads/feat"}})
	if err != nil {
		t.Fatal(err)
	}
	mergeOnRemote(t, lib, "feat", MergedCommit, true)

	if _, err := c.Sync(s, SyncOptions{Refs: true}); err != nil {
		t.Fatal(err)
	}
	if got := s.S.Members["lib"].Ref; got != "" || sha(t, filepath.Join(s.Dir, "_ref", "lib"), "HEAD") != sha(t, lib, "origin/main") {
		t.Errorf("ref = %q: want the package's again", got)
	}
}
