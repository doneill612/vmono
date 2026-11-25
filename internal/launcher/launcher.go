// Package launcher decides where a prepared harness runs.
//
// harness.Prepare turns a session into a ready-to-run invocation: the
// executable, its arguments, its working directory (the session root) and
// its environment. A launcher takes that invocation and puts it somewhere:
//
//   - direct runs it in the terminal vmono was started from. On macOS and
//     Linux vmono is replaced by the harness via exec; otherwise, and always
//     on Windows, the harness runs as a child and vmono waits for it. This is
//     the only behavior vmono had before launchers existed, and the default.
//   - tmux gives every vmono session its own tmux session, named after it,
//     and moves the user there: switch-client from inside tmux, attach from
//     outside. Opening a session that already has one goes straight to it
//     and never starts a second harness. Because the harness no longer takes
//     over vmono's terminal, the TUI can be used as a control panel, for
//     instance from a tmux popup that closes once the switch is made.
//   - herdr does the same with a herdr workspace per session.
//
// The launcher is chosen by the `launcher` config value (config.Launcher*).
// "auto" resolves to herdr or tmux when vmono runs inside one and to direct
// otherwise. Any multiplexer with a scriptable way to create a named place
// at a directory, run a command in it, find it again and focus it fits the
// same Launcher interface.
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/herdr"
)

// Result says what became of the terminal vmono was started from.
type Result int

const (
	// HandedOff means the terminal was given to the harness, or to tmux
	// attached to the harness's session, and is now free again: the process
	// exited or the client detached. When Opts.Replace was honored, Launch
	// does not return at all on success.
	HandedOff Result = iota
	// Elsewhere means the harness runs in another place, such as a tmux
	// session the client was switched to, or one that was started and left
	// alone because switching is turned off, and vmono's own terminal was
	// never given up. A TUI should simply quit.
	Elsewhere
)

// Opts carries the caller's choices for a launch.
type Opts struct {
	// Replace lets the launcher replace vmono, via exec, with the process it
	// hands the terminal to. It is ignored where exec is unsupported.
	Replace bool
	// RunDirect starts a harness in the current terminal. It defaults to
	// harness.Run; callers set it so tests can observe a direct launch
	// without replacing the test process.
	RunDirect func(l *harness.Launch, replace bool) error
}

// Launcher starts a prepared harness somewhere.
type Launcher interface {
	// Name is the config value that selects the launcher.
	Name() string
	// Launch starts the harness, or goes to the place where it already
	// runs, and reports what became of vmono's terminal.
	Launch(l *harness.Launch, opts Opts) (Result, error)
}

// lookPath finds an executable on PATH. It is a variable so tests can
// pretend tmux is or is not installed.
var lookPath = exec.LookPath

// Resolve returns the launcher the config asks for. "auto" picks herdr when
// vmono runs in a herdr pane, else tmux when it runs inside a tmux client ($TMUX
// is set), if the tool is on PATH, and direct otherwise, so a terminal outside
// both keeps working as before. An explicit launcher whose tool is missing is an
// error rather than a silent
// fallback, since the user asked for it by name.
func Resolve(cfg config.Config) (Launcher, error) {
	switch cfg.Launcher {
	case "", config.LauncherDirect:
		return Direct{}, nil
	case config.LauncherAuto:
		if herdr.Inside() && runtime.GOOS != "windows" {
			if _, err := lookPath("herdr"); err == nil {
				return Herdr{NoFocus: !cfg.HerdrFocus}, nil
			}
		}
		if os.Getenv("TMUX") != "" {
			if _, err := lookPath("tmux"); err == nil {
				return Tmux{NoSwitch: !cfg.TmuxSwitch}, nil
			}
		}
		return Direct{}, nil
	case config.LauncherHerdr:
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("launcher herdr is not supported on Windows")
		}
		if _, err := lookPath("herdr"); err != nil {
			return nil, fmt.Errorf("launcher herdr: herdr not found on PATH")
		}
		return Herdr{NoFocus: !cfg.HerdrFocus}, nil
	case config.LauncherTmux:
		if _, err := lookPath("tmux"); err != nil {
			return nil, fmt.Errorf("launcher tmux: tmux not found on PATH")
		}
		return Tmux{NoSwitch: !cfg.TmuxSwitch}, nil
	default:
		return nil, fmt.Errorf("unknown launcher %q", cfg.Launcher)
	}
}

// SelfLaunch returns the launch that runs a vmono command, rather than a
// harness, in a multiplexer: in dir, tagged "pkg/place", with the direct
// launcher so that vmono stays with the harness it starts there. argv is the
// command without the program, and name is what to call it in notices.
func SelfLaunch(name, dir, pkg, place string, argv []string) (*harness.Launch, error) {
	self, err := selfPath()
	if err != nil {
		return nil, err
	}
	// Anything that identifies the session vmono is asked from is blanked.
	overrides := map[string]string{"VMONO_LAUNCHER": config.LauncherDirect}
	for _, key := range []string{"VMONO_PACKAGE", "VMONO_SESSION", "VMONO_ROOT", "VMONO_BRANCH", "VMONO_AUTHOR"} {
		overrides[key] = ""
	}
	for _, kv := range settingsEnv() {
		key, val, _ := strings.Cut(kv, "=")
		overrides[key] = val
	}
	return &harness.Launch{Name: name, Dir: dir, Path: self, Args: argv, Package: pkg, Session: place, Overrides: overrides, Self: argv}, nil
}

// Launch resolves the configured launcher and starts l with it.
func Launch(cfg config.Config, l *harness.Launch, opts Opts) (Result, error) {
	ln, err := Resolve(cfg)
	if err != nil {
		return HandedOff, err
	}
	return ln.Launch(l, opts)
}

// Direct runs the harness in the current terminal.
type Direct struct{}

// Name implements Launcher.
func (Direct) Name() string { return config.LauncherDirect }

// Launch implements Launcher by running the harness in place. A harness's
// own non-zero exit comes back as a *harness.ExitError, as from harness.Run.
func (Direct) Launch(l *harness.Launch, opts Opts) (Result, error) {
	run := opts.RunDirect
	if run == nil {
		run = harness.Run
	}
	return HandedOff, run(l, opts.Replace)
}
