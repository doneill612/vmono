package config

import (
	"os"
	"path/filepath"
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
