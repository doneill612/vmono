// Package harness resolves and launches coding agent CLIs at a package
// root. Harnesses never integrate with vmono: vmono builds a directory they
// already know how to use, so launching one is just a working directory, an
// environment and an exec.
package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/manifest"
)

// Launch is a fully prepared harness invocation.
type Launch struct {
	// Name is the harness key in config, e.g. "claude".
	Name    string
	Harness config.Harness
	// Dir is the package root, used as the working directory.
	Dir string
	// Path is the resolved executable.
	Path string
	// Args are the harness's configured args followed by pass-through args.
	Args []string
	// Env is the complete environment for the process.
	Env []string
}

// ExitError carries a child harness's exit status so vmono can exit with
// the same code. It is not a vmono failure.
type ExitError struct{ Code int }

// Error implements error.
func (e *ExitError) Error() string { return fmt.Sprintf("harness exited with status %d", e.Code) }

// Resolve picks the harness to use: the flag, then the package's harness,
// then the global default.
func Resolve(cfg config.Config, m *manifest.Manifest, flag string) (string, config.Harness, error) {
	name := flag
	if name == "" && m != nil {
		name = m.Harness
	}
	if name == "" {
		name = cfg.DefaultHarness
	}
	if name == "" {
		return "", config.Harness{}, errors.New("no harness configured; pass --harness or set default_harness")
	}
	h, ok := cfg.Harness[name]
	if !ok {
		return "", config.Harness{}, core.NotFound("harness %q is not configured (have: %s)", name, strings.Join(cfg.HarnessNames(), ", "))
	}
	return name, h, nil
}

// Available reports whether a harness's command can be found on PATH. The
// TUI's picker uses it to mark harnesses that are not installed.
func Available(h config.Harness) bool {
	_, err := exec.LookPath(h.Command)
	return err == nil
}

// Prepare performs launch steps 1 to 4: resolve the harness, quick-sync the
// generated files (never touching git), append a launch record to the
// index, and build the working directory and environment.
func Prepare(c *core.Core, p *core.Package, harnessFlag string, extraArgs []string) (*Launch, error) {
	name, h, err := Resolve(c.Cfg, p.M, harnessFlag)
	if err != nil {
		return nil, err
	}
	path, err := exec.LookPath(h.Command)
	if err != nil {
		return nil, fmt.Errorf("harness %q: command %q not found on PATH", name, h.Command)
	}
	if _, err := c.Render(p); err != nil {
		return nil, fmt.Errorf("refresh generated files: %w", err)
	}
	c.Idx.RecordLaunch(p.Name, name, time.Now())
	_ = c.Idx.Save()

	args := append(append([]string(nil), h.Args...), extraArgs...)
	return &Launch{
		Name:    name,
		Harness: h,
		Dir:     p.Dir,
		Path:    path,
		Args:    args,
		Env:     buildEnv(os.Environ(), p, name, h),
	}, nil
}

// buildEnv layers vmono's variables and the harness's own env over base.
// Later entries win, and duplicates of overridden keys are dropped.
func buildEnv(base []string, p *core.Package, name string, h config.Harness) []string {
	overrides := map[string]string{
		"VMONO_PACKAGE": p.Name,
		"VMONO_ROOT":    p.Dir,
		"VMONO_AUTHOR":  "agent:" + name,
	}
	for k, v := range h.Env {
		overrides[k] = v
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if _, replaced := overrides[key]; !replaced {
			out = append(out, kv)
		}
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

// Run starts the harness. With replace set on macOS and Linux, vmono is
// replaced by the harness via exec. Otherwise (always on Windows) the
// harness runs as a child with inherited stdio and its exit status is
// returned as an *ExitError when non-zero.
func Run(l *Launch, replace bool) error {
	if replace {
		if err := execReplace(l); !errors.Is(err, errReplaceUnsupported) {
			return err
		}
	}
	return runChild(l)
}

// runChild runs the harness as a child process with inherited stdio.
func runChild(l *Launch) error {
	cmd := exec.Command(l.Path, l.Args...)
	cmd.Dir = l.Dir
	cmd.Env = l.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode()}
	}
	return err
}

// errReplaceUnsupported tells Run to fall back to a child process.
var errReplaceUnsupported = errors.New("exec replacement unsupported")
