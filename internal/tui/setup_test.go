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
		"[hooks]\npost_create = \"code .\"\n[hooks.post_finish]\ncommand = \"notify\"\ntimeout = \"30s\"\n")
	d := newDriver(t, env, "")

	d.key("c")
	d.has("Hooks", "post_create", "code .", "notify")
	d.key("down", "down")
	for range "code ." {
		d.key("backspace")
	}
	d.key("down")
	d.typeText("exit 1")
	d.key("ctrl+o")
	d.has("abort")
	d.key("enter")
	d.has("config saved")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]config.Hook{
		config.HookPreRemove:  {Command: "exit 1", OnFailure: config.OnFailureAbort},
		config.HookPostFinish: {Command: "notify", Timeout: "30s"},
	}
	if len(cfg.Hooks) != len(want) || cfg.Hooks[config.HookPreRemove] != want[config.HookPreRemove] || cfg.Hooks[config.HookPostFinish] != want[config.HookPostFinish] {
		t.Errorf("hooks = %+v", cfg.Hooks)
	}
	if d.app.env.core.Cfg.Hooks[config.HookPreRemove].Command != "exit 1" {
		t.Error("saved hooks should apply without a restart")
	}
}
