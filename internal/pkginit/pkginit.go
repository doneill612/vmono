// Package pkginit runs the initialization of a package: it checks the members
// out, starts a harness there to explore them, and takes the checkout away
// when the harness exits. The CLI and the TUI both drive it.
package pkginit

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/doneill612/vmono/internal/core"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/launcher"
	"github.com/doneill612/vmono/internal/render"
)

// Options tunes Run.
type Options struct {
	// Harness overrides the package's harness.
	Harness string
	// Launcher decides where the harness runs. Nil resolves the config's.
	Launcher launcher.Launcher
	// Replace lets a multiplexer replace vmono when it attaches to the place
	// the initialization runs in. vmono is never replaced by the harness.
	Replace bool
	// RunDirect runs the harness in this terminal and returns when it exits.
	// It defaults to harness.RunAndReturn; tests set it.
	RunDirect func(l *harness.Launch, replace bool) error
	// Events runs fn while reporting the core's progress. Nil reports nothing.
	Events func(fn func() error) error
	// Err is where notices for the user go; nil discards them.
	Err io.Writer
}

// Result says how an initialization ended.
type Result struct {
	// Launched is what became of the terminal; see launcher.Result.
	Launched launcher.Result
	// Direct is true when the harness ran in this terminal, so that the rest
	// of the result is known.
	Direct bool
	// Summary says in one line what was recorded.
	Summary string
}

// ErrAgent is returned when an agent asks for an initialization, which would
// start a second harness inside its own.
var ErrAgent = errors.New("an agent cannot initialize a package; run `vmono package init` from your own terminal")

// Run initializes a package.
//
// With the direct launcher the harness runs in this terminal as a child of
// vmono, whatever return_to_tui says, because vmono has to outlive it. With a
// multiplexer it is vmono itself that is started there, with the direct
// launcher, so cleaning up works the same way in every place; an
// initialization that is already running there is shown instead.
//
// A harness's own non-zero exit comes back as a *harness.ExitError after the
// cleanup. A workspace the harness changed is kept and reported in Summary.
func Run(c *core.Core, p *core.Package, opts Options) (Result, error) {
	if strings.HasPrefix(os.Getenv("VMONO_AUTHOR"), "agent:") {
		return Result{}, ErrAgent
	}
	name, h, err := harness.Resolve(c.Cfg, p.M, opts.Harness)
	if err != nil {
		return Result{}, err
	}
	if !harness.Available(h) {
		return Result{}, fmt.Errorf("harness %q: command %q not found on PATH", name, h.Command)
	}
	ln := opts.Launcher
	if ln == nil {
		if ln, err = launcher.Resolve(c.Cfg); err != nil {
			return Result{}, err
		}
	}
	if _, direct := ln.(launcher.Direct); !direct {
		// In the package directory, not the workspace, which comes and goes
		// under the shell.
		l, err := launcher.SelfLaunch(name, p.Dir, p.Name, core.InitName,
			[]string{"package", "init", p.Name, "--harness", name})
		if err != nil {
			return Result{}, err
		}
		res, err := ln.Launch(l, launcher.Opts{Replace: opts.Replace})
		return Result{Launched: res}, err
	}

	events, out := opts.Events, opts.Err
	if events == nil {
		events = func(fn func() error) error { return fn() }
	}
	if out == nil {
		out = io.Discard
	}
	var w *core.InitWorkspace
	if err := events(func() error {
		var oerr error
		w, oerr = c.OpenInit(p)
		return oerr
	}); err != nil {
		return Result{}, err
	}
	l, prompted, err := harness.PrepareInit(w, name, h, render.InitPrompt)
	if err == nil {
		if !prompted {
			fmt.Fprintf(out, "vmono: %s has no init_args, so send it this: %s\n", name, render.InitPrompt)
		}
		run := opts.RunDirect
		if run == nil {
			run = harness.RunAndReturn
		}
		err = run(l, false)
	}
	res := Result{Launched: launcher.HandedOff, Direct: true, Summary: summarize(c, p, w)}
	if cerr := events(func() error { return c.CloseInit(w, false) }); cerr != nil {
		kept := fmt.Sprintf("the temporary checkout at %s was kept: %v", w.Dir, cerr)
		if errors.Is(cerr, core.ErrRefused) {
			kept = fmt.Sprintf("files in the temporary checkout at %s were changed, so it was kept; `vmono package init %s --clean --force` removes it", w.Dir, p.Name)
		}
		res.Summary += "; " + kept
	}
	return res, err
}

// summarize says whether anything was recorded while w was open. The record
// is made by another vmono process, so the package is read again.
func summarize(c *core.Core, p *core.Package, w *core.InitWorkspace) string {
	now, err := c.Load(p.Name)
	if err != nil || now.M.Init == nil || now.M.Init.At.Before(w.Started) {
		return "nothing was recorded for " + p.Name
	}
	return "recorded an overview of " + p.Name
}
