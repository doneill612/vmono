package core

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
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

func TestGlobalHooks(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request("gl", []string{a}, nil))
	abort := func(cmd string) config.Hook { return config.Hook{Command: cmd, OnFailure: config.OnFailureAbort} }

	c.Cfg.Hooks = map[string]config.Hook{config.HookPostCreate: abort("exit 1")}
	if _, err := c.CreateSession(p, "one", SessionOptions{}); err == nil {
		t.Error("an aborting post_create must fail the create")
	}
	if _, err := os.Stat(p.SessionDir("one")); !os.IsNotExist(err) {
		t.Error("an aborted create must leave no session")
	}
	if gitx.BranchExists(a, "vmono/gl/one") {
		t.Error("an aborted create must leave no branch")
	}

	c.Cfg.Hooks = map[string]config.Hook{config.HookPostCreate: {Command: "exit 1"}}
	s, err := c.CreateSession(p, "one", SessionOptions{})
	if err != nil {
		t.Fatalf("a warning post_create must not fail the create: %v", err)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "work.txt")

	ref := "$VMONO_LANDED"
	if runtime.GOOS == "windows" {
		ref = "%VMONO_LANDED%"
	}
	c.Cfg.Hooks = map[string]config.Hook{
		config.HookPostFinish: {Command: "echo landed=" + ref},
		config.HookPreRemove:  abort("exit 1"),
	}
	events, err := record(c, func() error { _, err := c.Finish(s, FinishOptions{Close: true}); return err })
	if !errors.Is(err, ErrRefused) {
		t.Errorf("pre_remove must veto the close, got %v", err)
	}
	heard := false
	for _, ev := range events {
		heard = heard || ev.Status == apply.StatusOutput && strings.TrimSpace(ev.Message) == "landed=alpha:main"
	}
	if !heard {
		t.Errorf("post_finish output missing in %+v", events)
	}
	if _, err := c.Delete(p, DeleteOptions{Force: true}); !errors.Is(err, ErrRefused) {
		t.Errorf("pre_remove must veto a package delete, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha", "README.md")); err != nil {
		t.Fatal("a vetoed session must be intact")
	}
	c.Cfg.NoHooks = true
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Errorf("NoHooks must skip the veto: %v", err)
	}
}

func TestNoHooksSkipsSetup(t *testing.T) {
	c, env := newCore(t)
	c.Cfg.NoHooks = true
	m := request("quiet", []string{env.NewSource(t, "alpha")}, nil)
	m.Members[0].Setup = []string{"echo x > made.txt"}
	_, s := newWork(t, c, m)
	if _, err := os.Stat(filepath.Join(s.Dir, "alpha", "made.txt")); err == nil {
		t.Error("setup ran")
	}
}
