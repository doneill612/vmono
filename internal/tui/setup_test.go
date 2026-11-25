package tui

import (
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestSettingsEditHooks(t *testing.T) {
	env := testutil.NewEnv(t)
	seed(t, env, "pay", env.NewSource(t, "alpha"))
	testutil.WriteFile(t, filepath.Join(env.Root, "cfg", "config.toml"),
		"[hooks]\npost_create = \"code .\"\n[hooks.pre_remove]\ncommand = \"exit 1\"\ntimeout = \"30s\"\n")
	d := newDriver(t, env, "")

	d.key("c")
	d.has("Hooks", "post_create", "code .", "exit 1")
	d.key("down", "down")
	for range "code ." {
		d.key("backspace")
	}
	d.key("down")
	d.key("ctrl+o")
	d.has("abort")
	d.key("enter")
	d.has("config saved")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := config.Hook{Command: "exit 1", Timeout: "30s", OnFailure: config.OnFailureAbort}
	if len(cfg.Hooks) != 1 || cfg.Hooks[config.HookPreRemove] != want {
		t.Errorf("hooks = %+v", cfg.Hooks)
	}
	if d.app.env.core.Cfg.Hooks[config.HookPreRemove].Command != "exit 1" {
		t.Error("saved hooks should apply without a restart")
	}
}
