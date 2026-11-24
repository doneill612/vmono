package tui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/launcher"
	"github.com/doneill612/vmono/internal/testutil"
)

// liveDriver drives an app that watches agents, with a fake herdr on PATH
// answering `api snapshot` from a file. looks counts the snapshots taken.
func liveDriver(t *testing.T, env *testutil.Env, snapshot string) (d *driver, looks *int, fail *error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("herdr is not offered on Windows")
	}
	file := filepath.Join(env.Root, "snapshot.json")
	testutil.WriteFile(t, file, snapshot)
	bin := filepath.Join(env.Root, "bin", "herdr")
	testutil.WriteFile(t, bin, "#!/bin/sh\ncat '"+file+"'\n")
	if err := exec.Command("chmod", "+x", bin).Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	t.Setenv("PATH", filepath.Dir(bin)+":/usr/bin:/bin")
	orig, back := agentInterval, agentBackoff
	agentInterval, agentBackoff = time.Millisecond, time.Millisecond
	t.Cleanup(func() { agentInterval, agentBackoff = orig, back })

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Loaded = true
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	looks, fail = new(int), new(error)
	d = &driver{t: t, app: NewApp(c, "")}
	d.app.watchAgents(func() (launcher.Live, error) {
		*looks++
		if *fail != nil {
			return launcher.Live{}, *fail
		}
		return launcher.TakeLive()
	})
	d.send(tea.WindowSizeMsg{Width: 140, Height: 40})
	d.run(d.app.Init())
	return d, looks, fail
}

// agentsAt is a herdr snapshot with one agent per directory and state.
func agentsAt(pairs ...string) string {
	out := ""
	for i := 0; i < len(pairs); i += 2 {
		if out != "" {
			out += ","
		}
		out += `{"pane_id":"p","workspace_id":"w","cwd":"` + pairs[i] + `","agent_status":"` + pairs[i+1] + `"}`
	}
	return `{"result":{"snapshot":{"agents":[` + out + `]}}}`
}

func TestAgentStateInTheViews(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d, looks, fail := liveDriver(t, env, agentsAt(filepath.Join(env.PackageRoot, "pay", "work", "alpha"), "working"))

	d.has("agents", "1●")
	d.key("right")
	d.has("● working")

	// Updating under a working agent has to be asked for twice.
	d.key("u")
	d.has("u again to update anyway")
	d.key("u")
	d.eventually(func() bool { return containsView(d, "done") }, "the update")
	d.key("enter")

	// Nothing is looked at while no view shows it.
	d.key("m")
	before := *looks
	d.run(d.app.pollAgents())
	if *looks != before {
		t.Error("looked while the members view was up")
	}
	d.key("esc")
	d.run(d.app.pollAgents())
	if *looks != before+1 {
		t.Errorf("looks = %d, want %d", *looks, before+1)
	}

	// A failure keeps what was last seen and is reported once.
	*fail = errors.New("boom")
	d.run(d.app.pollAgents())
	d.has("could not read agent state: boom", "● working")
	d.send(flashMsg{})
	d.run(d.app.pollAgents())
	d.lacks("boom")
}

func TestNoAgentColumnWithoutASource(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	d := newDriver(t, env, "")
	d.lacks("agents")
	if d.app.pollAgents() != nil {
		t.Error("polling without a source")
	}
}
