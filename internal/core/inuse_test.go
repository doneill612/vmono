package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// blockCwd turns the working-directory check on for the test, whatever the
// platform, and restores it afterwards. The core tests are not parallel, so
// swapping the package variable is safe.
func blockCwd(t *testing.T) {
	t.Helper()
	prev := cwdBlocksRemoval
	cwdBlocksRemoval = true
	t.Cleanup(func() { cwdBlocksRemoval = prev })
}

// chdir changes the working directory for the rest of the test. It uses
// os.Chdir rather than t.Chdir on purpose: t.Chdir keeps the directory it
// leaves open until the test ends, and on Windows that open handle stops a
// later removal of that directory from succeeding. PWD is set as t.Chdir
// would, so symlinked temp dirs resolve as before.
func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PWD", dir)
}

// wantInUse fails the test unless err is an *InUseError for dir.
func wantInUse(t *testing.T, err error, dir string) {
	t.Helper()
	var ue *InUseError
	if !errors.As(err, &ue) {
		t.Fatalf("want *InUseError, got %v", err)
	}
	if ue.Dir != dir {
		t.Errorf("InUseError.Dir = %q, want %q", ue.Dir, dir)
	}
}

func TestDeleteSessionRefusedFromInsideEvenWhenForced(t *testing.T) {
	blockCwd(t)
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "inuse")

	chdir(t, filepath.Join(one.Dir, "alpha"))
	_, err := c.DeleteSession(one, DeleteOptions{Force: true})
	wantInUse(t, err, one.Dir)
	if _, err := os.Stat(one.Dir); err != nil {
		t.Errorf("a refused delete must remove nothing: %v", err)
	}

	if _, err := c.DeleteSession(two, DeleteOptions{Force: true}); err != nil {
		t.Fatalf("a sibling session is not in use: %v", err)
	}
	if _, err := os.Stat(two.Dir); !os.IsNotExist(err) {
		t.Error("the sibling session should be gone")
	}
	_ = p
}

func TestDeletePackageRefusedFromInsideASession(t *testing.T) {
	blockCwd(t)
	c, env := newCore(t)
	p, one, _, _ := twoSessions(t, c, env, "inuse")

	chdir(t, filepath.Join(one.Dir, "alpha"))
	_, err := c.Delete(p, DeleteOptions{Force: true})
	wantInUse(t, err, p.Dir)
	if _, err := os.Stat(p.Dir); err != nil {
		t.Errorf("the package must remain: %v", err)
	}
}

func TestRemoveMemberRefusedFromInsideItsWorktree(t *testing.T) {
	blockCwd(t)
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "inuse")
	b := env.NewSource(t, "beta")
	if _, err := c.AddMember(p, AddOptions{Source: b}); err != nil {
		t.Fatal(err)
	}

	chdir(t, filepath.Join(one.Dir, "beta"))
	_, err := c.RemoveMember(p, "beta", RemoveOptions{Force: true})
	wantInUse(t, err, filepath.Join(one.Dir, "beta"))
	for _, s := range []*Session{one, two} {
		if _, err := os.Stat(filepath.Join(s.Dir, "beta")); err != nil {
			t.Errorf("session %s must keep beta: %v", s.Name, err)
		}
	}
	if _, ok := p.M.Member("beta"); !ok {
		t.Error("the manifest must keep beta")
	}
}

func TestRemovalFromInsideIsUnchangedWhereTheOSAllowsIt(t *testing.T) {
	prev := cwdBlocksRemoval
	cwdBlocksRemoval = false
	t.Cleanup(func() { cwdBlocksRemoval = prev })
	c, env := newCore(t)
	_, one, _, _ := twoSessions(t, c, env, "inuse")

	chdir(t, env.Root)
	if err := checkNotInside(one.Dir); err != nil {
		t.Fatalf("the check must be off: %v", err)
	}
	chdir(t, filepath.Join(one.Dir, "alpha"))
	if err := checkNotInside(one.Dir); err != nil {
		t.Fatalf("the check must be off inside the session: %v", err)
	}
}
