package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsWhenMissing(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Loaded {
		t.Error("Loaded should be false when no file exists")
	}
	if cfg.PackageRoot != "~/.vmono" || cfg.BranchPrefix != "vmono/" || !cfg.AutoCommit || !cfg.FetchOnCreate {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if _, ok := cfg.Harness["claude"]; !ok {
		t.Error("built-in claude harness missing")
	}
}

func TestFileOverridesDefaultsIncludingFalse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	body := `
package_root = "/tmp/pk"
auto_commit = false
fetch_on_create = false
default_harness = "x"

[harness.x]
command = "xcmd"
`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Loaded || cfg.PackageRoot != "/tmp/pk" || cfg.AutoCommit || cfg.FetchOnCreate {
		t.Errorf("file values not applied: %+v", cfg)
	}
	if cfg.Harness["x"].Command != "xcmd" {
		t.Errorf("custom harness missing: %+v", cfg.Harness)
	}
}

func TestEnvBeatsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(`package_root = "/from/file"`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VMONO_PACKAGE_ROOT", "/from/env")
	t.Setenv("VMONO_AUTO_COMMIT", "false")
	cfg, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PackageRoot != "/from/env" || cfg.AutoCommit {
		t.Errorf("env not applied: %+v", cfg)
	}
}

func TestInvalidDefaultHarness(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(`default_harness = "ghost"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(file); err == nil {
		t.Fatal("expected validation error")
	}
}

// TestLauncherFromFileAndEnv pins that the launcher defaults to direct, is
// read from the file, is overridden by VMONO_LAUNCHER, and that an unknown
// value is refused at load rather than failing later at launch.
func TestLauncherFromFileAndEnv(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Launcher != LauncherDirect {
		t.Errorf("default launcher = %q, want %q", cfg.Launcher, LauncherDirect)
	}

	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(`launcher = "auto"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadFrom(file); err != nil {
		t.Fatal(err)
	}
	if cfg.Launcher != LauncherAuto {
		t.Errorf("file launcher = %q, want %q", cfg.Launcher, LauncherAuto)
	}

	t.Setenv("VMONO_LAUNCHER", "tmux")
	if cfg, err = LoadFrom(file); err != nil {
		t.Fatal(err)
	}
	if cfg.Launcher != LauncherTmux {
		t.Errorf("env launcher = %q, want %q", cfg.Launcher, LauncherTmux)
	}

	t.Setenv("VMONO_LAUNCHER", "screen")
	if _, err := LoadFrom(file); err == nil {
		t.Error("expected an unknown launcher to be refused")
	}
}

// TestTmuxSwitchDefaultsOnAndCanBeTurnedOff pins that switching to a
// session's tmux session is the default, that an explicit false in the file
// overrides it, and that VMONO_TMUX_SWITCH beats the file.
func TestTmuxSwitchDefaultsOnAndCanBeTurnedOff(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || !cfg.TmuxSwitch {
		t.Fatalf("default tmux_switch = %v, %v; want true", cfg.TmuxSwitch, err)
	}
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte("tmux_switch = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadFrom(file); err != nil || cfg.TmuxSwitch {
		t.Errorf("file tmux_switch = %v, %v; want false", cfg.TmuxSwitch, err)
	}
	t.Setenv("VMONO_TMUX_SWITCH", "true")
	if cfg, err = LoadFrom(file); err != nil || !cfg.TmuxSwitch {
		t.Errorf("env tmux_switch = %v, %v; want true", cfg.TmuxSwitch, err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg := Defaults()
	cfg.SourceRoots = []string{"~/source"}
	cfg.AutoCommit = false
	if err := cfg.SaveTo(file); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.AutoCommit || len(got.SourceRoots) != 1 || got.SourceRoots[0] != "~/source" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

// TestSavedHarnessTableGetsTheBuiltInAdapter pins that a config file written
// before codex and pi had an adapter still gets one, while a harness
// repointed at another command is left alone.
func TestSavedHarnessTableGetsTheBuiltInAdapter(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	body := `
[harness.codex]
command = "codex"
[harness.pi]
command = "pi"
[harness.mine]
command = "my-agent"
[harness.claude]
command = "claude-wrapper"
`
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"codex": "agent-skills", "pi": "agent-skills", "mine": "", "claude": ""} {
		if got := cfg.Harness[name].Adapter; got != want {
			t.Errorf("%s adapter = %q, want %q", name, got, want)
		}
	}
}

func TestHooksRoundTripAndValidate(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	load := func(text string) (Config, error) {
		if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return LoadFrom(file)
	}
	cfg, err := load("[hooks]\npost_create = \"code .\"\n[hooks.pre_remove]\ncommand = \"x\"\ntimeout = \"30s\"\non_failure = \"abort\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveTo(file); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{cfg, again} {
		if c.Hooks[HookPostCreate].Command != "code ." || c.Hooks[HookPreRemove] != (Hook{Command: "x", Timeout: "30s", OnFailure: OnFailureAbort}) {
			t.Errorf("hooks = %+v", c.Hooks)
		}
	}
	for _, bad := range []string{
		"[hooks]\npost_creat = \"x\"\n", "[hooks]\npost_create = \"\"\n", "[hooks]\npost_create = 3\n",
		"[hooks.pre_remove]\ncommand = \"x\"\ntimeout = \"soon\"\n", "[hooks.pre_remove]\ncommand = \"x\"\non_failure = \"explode\"\n",
		"[hooks.pre_remove]\ncommand = \"x\"\nonfailure = \"abort\"\n",
	} {
		if _, err := load(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := Defaults().SaveTo(file); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); strings.Contains(string(data), "hooks") {
		t.Errorf("no hooks should write no table:\n%s", data)
	}
}

func TestSavedHarnessesGetInitArgs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	text := "[harness.claude]\ncommand = \"claude\"\nadapter = \"claude-code\"\n[harness.codex]\ncommand = \"my-codex\"\n[harness.pi]\ncommand = \"pi\"\ninit_args = [\"mine\"]\n"
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Harness["claude"].InitArgs; len(got) == 0 || got[len(got)-1] != PromptArg {
		t.Errorf("claude = %v", got)
	}
	if got := cfg.Harness["codex"].InitArgs; got != nil {
		t.Errorf("a repointed harness is left alone: %v", got)
	}
	if got := cfg.Harness["pi"].InitArgs; len(got) != 1 || got[0] != "mine" {
		t.Errorf("the user's own args win: %v", got)
	}
}
