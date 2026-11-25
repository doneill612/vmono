package core

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/index"
	"github.com/doneill612/vmono/internal/testutil"
)

// TestRemovingASessionClosesItsTerminalLast pins the order: the terminal is
// found while the session exists, and closed once the session is gone and the
// index is saved, since closing it may end vmono itself.
func TestRemovingASessionClosesItsTerminalLast(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p, s := newWork(t, c, request("cl", []string{a}, nil))
	two, err := c.CreateSession(p, "two", SessionOptions{Branch: "vmono/cl-two"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sess := range []*Session{s, two} {
		c.Idx.RecordLaunch("cl", sess.Name, "claude", sess.S.Created)
	}
	_ = c.Idx.Save()
	var found, closed []string
	c.FindTerminal = func(pkg, session, dir string) (func() []string, bool) {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s must be found before it goes", session)
		}
		found = append(found, session)
		return func() []string {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("%s must be gone before its terminal is closed", session)
			}
			idx, err := index.Load()
			if err != nil || !idx.SessionOpened("cl", session).IsZero() {
				t.Errorf("%s must be out of the saved index: %v", session, err)
			}
			closed = append(closed, session)
			return []string{"closed it"}
		}, session == "work" // the one vmono runs in
	}

	testutil.WriteFile(t, s.Dir+"/alpha/wip.txt", "x")
	if _, err := c.DeleteSession(s, DeleteOptions{}); !errors.Is(err, ErrRefused) || len(found) != 0 {
		t.Errorf("a refused removal must touch nothing: %v, %v", found, err)
	}
	if _, err := c.DeleteSession(two, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Delete(p, DeleteOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(closed, " ") != "two "+InitName+" work" {
		t.Errorf("closed = %v", closed)
	}
}
