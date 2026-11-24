package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/testutil"
)

// record runs fn with the core's events collected.
func record(c *Core, fn func() error) ([]apply.Event, error) {
	ch := make(chan apply.Event, 64)
	done := make(chan []apply.Event)
	go func() {
		var all []apply.Event
		for ev := range ch {
			all = append(all, ev)
		}
		done <- all
	}()
	c.Events = ch
	err := fn()
	c.Events = nil
	close(ch)
	return <-done, err
}

func TestMemberSetupAndTeardown(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	log := filepath.Join(env.Root, "torn.txt")
	m := request("hooked", []string{a}, nil)
	m.Members[0].Setup = []string{"echo ready", "git rev-parse HEAD > .git-made", "exit 3", "echo unreached"}
	m.Members[0].Teardown = []string{"echo gone>" + filepath.ToSlash(log)}
	testutil.WriteFile(t, filepath.Join(a, ".gitignore"), ".git-made\n")
	testutil.Git(t, a, "add", "-A")
	testutil.Git(t, a, "commit", "--quiet", "-m", "ignore")
	testutil.Git(t, a, "push", "--quiet")
	p := mustPackage(t, c, m)

	var s *Session
	events, err := record(c, func() (err error) { s, err = c.CreateSession(p, "work", SessionOptions{}); return err })
	if err != nil {
		t.Fatalf("a failing setup must not fail the session: %v", err)
	}
	var out, warned string
	for _, ev := range events {
		if ev.Member == "alpha" && ev.Step == "setup" {
			switch ev.Status {
			case apply.StatusOutput:
				out += ev.Message + "\n"
			case apply.StatusWarn:
				warned += ev.Message
			}
		}
	}
	if out != "ready\n" || !strings.Contains(warned, "exit 3") {
		t.Errorf("output = %q, warning = %q", out, warned)
	}
	made := filepath.Join(s.Dir, "alpha", ".git-made")
	if _, err := os.Stat(made); err != nil {
		t.Errorf("setup should run in the worktree: %v", err)
	}

	// A recreated worktree is set up again.
	testutil.Git(t, a, "worktree", "remove", "--force", filepath.Join(s.Dir, "alpha"))
	if _, err := c.Sync(s, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(made); err != nil {
		t.Errorf("sync should set the recreated worktree up: %v", err)
	}

	if _, err := os.Stat(log); err == nil {
		t.Fatal("teardown must not run before removal")
	}
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log); err != nil {
		t.Errorf("teardown should have run: %v", err)
	}
}
