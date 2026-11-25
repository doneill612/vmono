// Package harness resolves and launches coding agent CLIs at a session
// root. Harnesses never integrate with vmono: vmono builds a directory they
// already know how to use, so launching one is just a working directory, an
// environment and an exec.
package harness

import (
	"errors"
	"fmt"
	"maps"
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
	// Package and Session name the session being launched. A launcher that
	// starts the harness somewhere other than the current terminal, such as
	// a tmux session, uses them to name and later find that place.
	Package string
	Session string
	// Overrides are the variables vmono sets on top of the inherited
	// environment: the VMONO_* variables and the harness's own env. Env
	// already contains them; they are kept apart for launchers that hand the
	// process a fresh environment of their own, such as tmux, which starts
	// it from its server's environment and only adds what it is told.
	Overrides map[string]string
	// Self, when set, is the vmono command line, without the program, that
	// starts the same thing again in the current terminal. A launcher that
	// types a command, or suggests one, uses it instead of `open`.
	Self []string
}

// ExitError carries a child harness's exit status so vmono can exit with
// the same code. It is not a vmono failure.
type ExitError struct{ Code int }

// Error implements error.
func (e *ExitError) Error() string { return fmt.Sprintf("harness exited with status %d", e.Code) }

// Resolve picks the harness to use: the flag, then the package's harness,
// then the global default.
func Resolve(cfg config.Config, m *manifest.Manifest, flag string) (string, config.Harness, error) {
	pkgHarness := ""
	if m != nil {
		pkgHarness = m.Harness
	}
	name, h, err := cfg.ResolveHarness(flag, pkgHarness)
	var unknown *config.UnknownHarnessError
	if errors.As(err, &unknown) {
		return "", config.Harness{}, core.NotFound("%s", unknown.Error())
	}
	return name, h, err
}

// Available reports whether a harness's command can be found on PATH. The
// TUI's picker uses it to mark harnesses that are not installed.
func Available(h config.Harness) bool {
	_, err := exec.LookPath(h.Command)
	return err == nil
}

// Prepare performs launch steps 1 to 4: resolve the harness, quick-sync the
// session's generated files (never touching git), append a launch record to
// the index, and build the working directory, the session root, and the
// environment.
func Prepare(c *core.Core, s *core.Session, harnessFlag string, extraArgs []string) (*Launch, error) {
	name, h, err := Resolve(c.Cfg, s.Pkg.M, harnessFlag)
	if err != nil {
		return nil, err
	}
	path, err := lookup(name, h)
	if err != nil {
		return nil, err
	}
	if _, err := c.Render(s); err != nil {
		return nil, fmt.Errorf("refresh generated files: %w", err)
	}
	c.Idx.RecordLaunch(s.Pkg.Name, s.Name, name, time.Now())
	_ = c.Idx.Save()

	args := append(append([]string(nil), h.Args...), extraArgs...)
	return &Launch{
		Name:      name,
		Harness:   h,
		Dir:       s.Dir,
		Path:      path,
		Args:      args,
		Env:       buildEnv(os.Environ(), envOverrides(core.SessionEnv(s), name, h)),
		Package:   s.Pkg.Name,
		Session:   s.Name,
		Overrides: envOverrides(core.SessionEnv(s), name, h),
	}, nil
}

// PrepareInit builds the invocation that explores an init workspace: the
// harness at the workspace, its init arguments with prompt filled in, and an
// environment that names the package but no session, even when vmono itself
// runs in one. Nothing is recorded in the index, since no session is opened.
// prompted is false when the harness has no init arguments to carry the
// prompt, so the user has to paste it.
func PrepareInit(w *core.InitWorkspace, name string, h config.Harness, prompt string) (l *Launch, prompted bool, err error) {
	path, err := lookup(name, h)
	if err != nil {
		return nil, false, err
	}
	args := append([]string(nil), h.Args...)
	for _, a := range h.InitArgs {
		prompted = prompted || strings.Contains(a, config.PromptArg)
		args = append(args, strings.ReplaceAll(a, config.PromptArg, prompt))
	}
	overrides := envOverrides(map[string]string{
		"VMONO_PACKAGE": w.Pkg.Name, "VMONO_ROOT": w.Dir, "VMONO_SESSION": "", "VMONO_BRANCH": "",
	}, name, h)
	return &Launch{
		Name: name, Harness: h, Dir: w.Dir, Path: path, Args: args,
		Env:     buildEnv(os.Environ(), overrides),
		Package: w.Pkg.Name, Session: core.InitName, Overrides: overrides,
	}, prompted, nil
}

// lookup finds a harness's executable.
func lookup(name string, h config.Harness) (string, error) {
	path, err := exec.LookPath(h.Command)
	if err != nil {
		return "", fmt.Errorf("harness %q: command %q not found on PATH", name, h.Command)
	}
	return path, nil
}

// envOverrides returns the variables vmono sets for a harness: identity, which
// says what it is working in, the author stamped on notes it writes, then the harness's own
// env from config, which wins over vmono's on a clash.
func envOverrides(identity map[string]string, name string, h config.Harness) map[string]string {
	overrides := maps.Clone(identity)
	overrides["VMONO_AUTHOR"] = "agent:" + name
	for k, v := range h.Env {
		overrides[k] = v
	}
	return overrides
}

// buildEnv layers overrides over base.
// Later entries win, and duplicates of overridden keys are dropped.
func buildEnv(base []string, overrides map[string]string) []string {
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
	return exitError(command(l).Run())
}

// command builds the child process for a launch, with inherited stdio.
func command(l *Launch) *exec.Cmd {
	cmd := exec.Command(l.Path, l.Args...)
	cmd.Dir = l.Dir
	cmd.Env = l.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// exitError turns a child's non-zero exit into an *ExitError.
func exitError(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode()}
	}
	return err
}

// errReplaceUnsupported tells Run to fall back to a child process.
var errReplaceUnsupported = errors.New("exec replacement unsupported")
