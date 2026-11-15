package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/testutil"
)

// publishBranch pushes a new branch to src's remote from a throwaway clone
// so a later fetch in src has something to bring in.
func publishBranch(t *testing.T, env *testutil.Env, src, branch string) {
	t.Helper()
	remote := testutil.Git(t, src, "remote", "get-url", "origin")
	clone := filepath.Join(env.Root, "pusher", filepath.Base(src)+"-"+branch)
	testutil.Git(t, env.Root, "clone", "--quiet", remote, clone)
	testutil.Git(t, clone, "checkout", "--quiet", "-b", branch)
	testutil.WriteFile(t, filepath.Join(clone, branch+".txt"), branch)
	testutil.Git(t, clone, "add", "-A")
	testutil.Git(t, clone, "commit", "--quiet", "-m", branch)
	testutil.Git(t, clone, "push", "--quiet", "origin", branch)
}

// collect runs fn with an event channel attached and returns every event it
// produced.
func collect(c *Core, fn func()) []apply.Event {
	ch := make(chan apply.Event, 256)
	c.Events = ch
	fn()
	close(ch)
	c.Events = nil
	var evs []apply.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

func TestCreateFetchesAllSourcesInOneStepFirst(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	ref := env.NewSource(t, "contracts")
	for _, src := range []string{a, b, ref} {
		publishBranch(t, env, src, "feature")
	}

	var err error
	evs := collect(c, func() {
		_, err = c.Create(request("fetchy", []string{a, b}, []string{ref}), CreateOptions{})
	})
	if err != nil {
		t.Fatal(err)
	}

	var fetchRuns int
	for i, ev := range evs {
		if ev.Step == "fetch" && ev.Status == apply.StatusRunning {
			fetchRuns++
			if i != 0 || ev.Member != "" {
				t.Errorf("fetch must be the first, package-level step: index %d, member %q", i, ev.Member)
			}
		}
	}
	if fetchRuns != 1 {
		t.Errorf("want exactly one fetch step, got %d", fetchRuns)
	}
	for _, src := range []string{a, b, ref} {
		if !gitx.RefExists(src, "origin/feature") {
			t.Errorf("%s should have been fetched", src)
		}
	}
}

func TestCreateWithFetchOnCreateOffSkipsFetch(t *testing.T) {
	c, env := newCore(t)
	c.Cfg.FetchOnCreate = false
	a := env.NewSource(t, "alpha")
	publishBranch(t, env, a, "feature")

	var err error
	evs := collect(c, func() {
		_, err = c.Create(request("nofetch", []string{a}, nil), CreateOptions{})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if ev.Step == "fetch" {
			t.Fatalf("unexpected fetch event: %+v", ev)
		}
	}
	if gitx.RefExists(a, "origin/feature") {
		t.Error("source must not be fetched when fetch_on_create is off")
	}
}

func TestCreateContinuesWhenAFetchFails(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	publishBranch(t, env, a, "feature")
	testutil.Git(t, b, "remote", "set-url", "origin", filepath.Join(env.Root, "remotes", "gone.git"))

	var err error
	evs := collect(c, func() {
		_, err = c.Create(request("partial", []string{a, b}, nil), CreateOptions{})
	})
	if err != nil {
		t.Fatalf("a failed fetch must not fail the create: %v", err)
	}
	var warned bool
	for _, ev := range evs {
		if ev.Step == "fetch" && ev.Status == apply.StatusWarn && strings.Contains(ev.Message, "fetch failed for ") && strings.Contains(ev.Message, filepath.Base(b)) && !strings.Contains(ev.Message, "\n") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a fetch warning naming %s: %+v", filepath.Base(b), evs)
	}
	if !gitx.RefExists(a, "origin/feature") {
		t.Error("the healthy source should still have been fetched")
	}
}

func TestFetchSourcesFoldsAuthFailuresIntoOneHint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake ssh")
	}
	env := testutil.NewEnv(t)
	fake := filepath.Join(env.Root, "fakessh")
	testutil.WriteFile(t, fake, "#!/bin/sh\necho 'git@host: Permission denied (publickey).' >&2\nexit 255\n")
	chmodExec(t, fake)
	t.Setenv("GIT_SSH_COMMAND", fake)

	a := env.NewSource(t, "alpha")
	b := env.NewSource(t, "beta")
	for _, src := range []string{a, b} {
		testutil.Git(t, src, "remote", "set-url", "origin", "ssh://git@example.invalid/"+filepath.Base(src)+".git")
	}

	var msgs []string
	fetchSources([]string{a, b}, func(m string) { msgs = append(msgs, m) })
	if len(msgs) != 1 {
		t.Fatalf("want one consolidated warning, got %d: %v", len(msgs), msgs)
	}
	for _, want := range []string{a, b, gitx.AuthHint} {
		if !strings.Contains(msgs[0], want) {
			t.Errorf("warning missing %q: %s", want, msgs[0])
		}
	}
}

// chmodExec marks path executable.
func chmodExec(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
