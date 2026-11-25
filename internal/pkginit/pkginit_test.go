package pkginit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/launcher"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// setup builds a package "demo" and a harness "fake" that is on PATH
// everywhere the tests run.
func setup(t *testing.T) (*core.Core, *core.Package) {
	t.Helper()
	env := testutil.NewEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DefaultHarness = "fake"
	cfg.Harness["fake"] = config.Harness{Command: "git", InitArgs: []string{config.PromptArg}}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.CreatePackage(&manifest.Manifest{Name: "demo", Members: []manifest.Member{{Source: env.NewSource(t, "alpha"), Mode: manifest.ModeActive, Setup: manifest.Commands{"touch ran-setup"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return c, p
}

func TestRunDirectRecordsAndCleansUp(t *testing.T) {
	c, p := setup(t)
	res, err := Run(c, p, Options{RunDirect: func(l *harness.Launch, replace bool) error {
		if replace {
			t.Error("vmono must outlive the harness")
		}
		if _, err := os.Stat(filepath.Join(l.Dir, "_ref", "alpha", "README.md")); err != nil {
			t.Error(err)
		}
		if text, _ := os.ReadFile(filepath.Join(l.Dir, "AGENTS.md")); !strings.Contains(string(text), "alpha setup: `touch ran-setup`") {
			t.Errorf("the agent should see the commands members have:\n%s", text)
		}
		if _, err := os.Stat(filepath.Join(l.Dir, "_ref", "alpha", "ran-setup")); err == nil {
			t.Error("setup commands must not run")
		}
		again, _ := c.Load("demo")
		_, err := c.RecordInit(again, core.InitRecord{Overview: "o"})
		return err
	}})
	if err != nil || !res.Direct || res.Summary != "recorded an overview of demo" {
		t.Errorf("run = %+v, %v", res, err)
	}
	if c.FindInit(p) != nil {
		t.Error("the workspace should be gone")
	}
}

func TestRunDirectCleansUpAfterAFailure(t *testing.T) {
	c, p := setup(t)
	c.Cfg.Harness["fake"] = config.Harness{Command: "git"}
	var notice bytes.Buffer
	var ee *harness.ExitError
	res, err := Run(c, p, Options{Err: &notice, RunDirect: func(*harness.Launch, bool) error { return &harness.ExitError{Code: 3} }})
	if !errors.As(err, &ee) || ee.Code != 3 || res.Summary != "nothing was recorded for demo" {
		t.Errorf("run = %+v, %v", res, err)
	}
	if c.FindInit(p) != nil {
		t.Error("the workspace should be gone")
	}
	if !strings.Contains(notice.String(), "AGENTS.md") {
		t.Errorf("with no init_args the prompt should be shown: %q", notice.String())
	}
}

func TestRunKeepsAWorkspaceTheHarnessChanged(t *testing.T) {
	c, p := setup(t)
	res, err := Run(c, p, Options{RunDirect: func(l *harness.Launch, _ bool) error {
		testutil.WriteFile(t, filepath.Join(l.Dir, "_ref", "alpha", "new"), "x")
		return nil
	}})
	if err != nil || !strings.Contains(res.Summary, "--clean --force") || c.FindInit(p) == nil {
		t.Errorf("run = %+v, %v", res, err)
	}
}

// place is a launcher that is not direct.
type place struct{ got *harness.Launch }

func (*place) Name() string { return "place" }
func (f *place) Launch(l *harness.Launch, _ launcher.Opts) (launcher.Result, error) {
	f.got = l
	return launcher.Elsewhere, nil
}

func TestRunElsewhereStartsVmonoItself(t *testing.T) {
	c, p := setup(t)
	t.Setenv("VMONO_SESSION", "outer")
	f := &place{}
	res, err := Run(c, p, Options{Launcher: f, RunDirect: func(*harness.Launch, bool) error {
		t.Error("must not run here")
		return nil
	}})
	if err != nil || res.Direct || res.Launched != launcher.Elsewhere {
		t.Fatalf("run = %+v, %v", res, err)
	}
	want := []string{"package", "init", "demo", "--harness", "fake"}
	if !slices.Equal(f.got.Args, want) || !slices.Equal(f.got.Self, want) || f.got.Dir != p.Dir || f.got.Session != ".init" {
		t.Errorf("launch = %+v", f.got)
	}
	o := f.got.Overrides
	if v, ok := o["VMONO_SESSION"]; !ok || v != "" || o["VMONO_LAUNCHER"] != "direct" || o["VMONO_PACKAGE_ROOT"] == "" {
		t.Errorf("overrides = %v", o)
	}
	if c.FindInit(p) != nil {
		t.Error("the workspace is made there, not here")
	}
}

func TestRunRefusesAnAgent(t *testing.T) {
	c, p := setup(t)
	t.Setenv("VMONO_AUTHOR", "agent:claude")
	if _, err := Run(c, p, Options{}); !errors.Is(err, ErrAgent) {
		t.Errorf("err = %v", err)
	}
}
