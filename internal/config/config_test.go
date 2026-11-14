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
