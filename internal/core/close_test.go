package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestRemovingASessionClosesItsTerminalFirst(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("cl", []string{a}, nil))
	two, err := c.CreateSession(p, "two", SessionOptions{Branch: "vmono/cl-two"})
	if err != nil {
		t.Fatal(err)
	}
	var closed []string
	c.CloseSession = func(pkg, session, dir string) []string {
		if _, err := os.Stat(filepath.Join(dir, "alpha")); err != nil {
			t.Errorf("%s must be closed before its worktrees go", session)
		}
		closed = append(closed, pkg+"/"+session)
		return []string{"closed it"}
	}

	testutil.WriteFile(t, filepath.Join(s.Dir, "alpha", "wip.txt"), "x")
	if _, err := c.DeleteSession(s, DeleteOptions{}); !errors.Is(err, ErrRefused) || len(closed) != 0 {
		t.Errorf("a refused removal must close nothing: %v, %v", closed, err)
	}
	if _, err := c.Finish(two, FinishOptions{Close: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Delete(p, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if len(closed) != 2 || closed[0] != "cl/two" || closed[1] != "cl/work" {
		t.Errorf("closed = %v", closed)
	}
}
