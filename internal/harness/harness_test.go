package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestResolvePrecedence(t *testing.T) {
	cfg := config.Defaults()
	m := &manifest.Manifest{Harness: "opencode"}
	if name, _, err := Resolve(cfg, m, "codex"); err != nil || name != "codex" {
		t.Errorf("flag should win: %q, %v", name, err)
	}
	if name, _, _ := Resolve(cfg, m, ""); name != "opencode" {
		t.Errorf("manifest should beat default: %q", name)
	}
	if name, _, _ := Resolve(cfg, &manifest.Manifest{}, ""); name != "claude" {
		t.Errorf("default harness: %q", name)
	}
	if _, _, err := Resolve(cfg, m, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("unknown harness should be not-found: %v", err)
	}
}

// fakeSetup creates a package and a harness named "fake" running a shell
// snippet, and returns what Prepare needs.
func fakeSetup(t *testing.T, script string) (*core.Core, *core.Package) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	env := testutil.NewEnv(t)
	cfg := config.Defaults()
	cfg.DefaultHarness = "fake"
	cfg.Harness["fake"] = config.Harness{Command: "sh", Args: []string{"-c", script, "sh"}, Env: map[string]string{"FROM_HARNESS": "yes"}}
	c, err := core.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a := env.NewSource(t, "alpha")
	p, err := c.Create(&manifest.Manifest{Name: "pkg", Members: []manifest.Member{{Source: a, Mode: manifest.ModeActive}}}, core.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return c, p
}

func TestPrepareAndRunChild(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	c, p := fakeSetup(t, `printf '%s|%s|%s|%s|%s' "$VMONO_PACKAGE" "$VMONO_ROOT" "$VMONO_AUTHOR" "$FROM_HARNESS" "$(pwd)" > "$OUT_FILE"; exit 7`)
	t.Setenv("OUT_FILE", out)

	if err := os.Remove(filepath.Join(p.Dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	l, err := Prepare(c, p, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "AGENTS.md")); err != nil {
		t.Error("launch must quick-sync generated files")
	}
	if len(c.Idx.Launches) != 1 || c.Idx.Launches[0].Harness != "fake" || c.Idx.Packages["pkg"].LastOpened.IsZero() {
		t.Errorf("launch not recorded: %+v", c.Idx.Launches)
	}

	err = Run(l, false)
	var he *ExitError
	if !errors.As(err, &he) || he.Code != 7 {
		t.Fatalf("want ExitError 7, got %v", err)
	}
	data, _ := os.ReadFile(out)
	parts := strings.Split(string(data), "|")
	if len(parts) != 5 || parts[0] != "pkg" || parts[2] != "agent:fake" || parts[3] != "yes" {
		t.Fatalf("environment wrong: %q", data)
	}
	if rootEval, _ := filepath.EvalSymlinks(p.Dir); parts[1] != p.Dir {
		t.Errorf("VMONO_ROOT = %q, want %q", parts[1], p.Dir)
	} else if cwd, _ := filepath.EvalSymlinks(parts[4]); cwd != rootEval {
		t.Errorf("cwd = %q, want %q", cwd, rootEval)
	}
}

func TestPassThroughArgsAndMissingCommand(t *testing.T) {
	c, p := fakeSetup(t, `printf '%s' "$1" > "$OUT_FILE"`)
	out := filepath.Join(t.TempDir(), "o")
	t.Setenv("OUT_FILE", out)
	l, err := Prepare(c, p, "", []string{"--resume"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(l, false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != "--resume" {
		t.Errorf("pass-through arg = %q", data)
	}

	c.Cfg.Harness["gone"] = config.Harness{Command: "definitely-not-installed-xyz"}
	if Available(c.Cfg.Harness["gone"]) {
		t.Error("missing command reported available")
	}
	if _, err := Prepare(c, p, "gone", nil); err == nil {
		t.Error("expected error for missing command")
	}
}

func TestBuildEnvOverridesInherited(t *testing.T) {
	p := &core.Package{Name: "n", Dir: "/d"}
	env := buildEnv([]string{"A=1", "VMONO_PACKAGE=stale", "VMONO_AUTHOR=user"}, p, "claude", config.Harness{})
	var pkg, author []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "VMONO_PACKAGE=") {
			pkg = append(pkg, kv)
		}
		if strings.HasPrefix(kv, "VMONO_AUTHOR=") {
			author = append(author, kv)
		}
	}
	if len(pkg) != 1 || pkg[0] != "VMONO_PACKAGE=n" || len(author) != 1 || author[0] != "VMONO_AUTHOR=agent:claude" {
		t.Errorf("env = %v", env)
	}
}
