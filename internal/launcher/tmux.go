package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
)

// TagOption is the tmux session option vmono sets on every session it
// creates, holding the vmono session it belongs to as "pkg/session". Sessions
// are found by this tag rather than by name, so a session the user renamed
// is still found, and one of the user's own that happens to share the name is
// never mistaken for vmono's.
const TagOption = "@vmono_session"

// Tmux runs each vmono session's harness in a tmux session of its own.
//
// The tmux session is named after the vmono session, "pkg/session", with the
// characters tmux forbids in names ('.' and ':') replaced by '_', and tagged
// with TagOption. Launching:
//
//  1. looks for a tmux session tagged with this vmono session, and if there
//     is none, creates one detached at the session root running the harness,
//     then tags it;
//  2. moves the user there: inside tmux the current client is switched to
//     it, and vmono's own terminal is left alone (Elsewhere); outside tmux
//     vmono attaches to it, replacing itself with `tmux attach` when
//     Opts.Replace allows (HandedOff).
//
// With NoSwitch (the `tmux_switch = false` setting), step 2 is replaced by
// a notice: the user stays in the tmux session they are in, or at their
// shell, and is told the name of the session that was started or found.
// Inside tmux the notice is shown in the client's status line with
// `display-message`, since vmono may be running in a popup whose own output
// disappears as it closes; it is also printed to standard error, which is
// where it appears outside tmux. The result is Elsewhere, so a TUI quits.
//
// An existing session is reused as is, so arguments passed through
// `vmono open -- ARGS` only apply when the session is created. The harness is
// the session's only process: when it exits, tmux closes the session, just as
// a direct launch returns to the shell.
//
// tmux starts the harness from its server's environment, not vmono's, so the
// VMONO_* variables, the harness's own env and vmono's PATH are passed with
// `new-session -e`. PATH is included so the agent finds the same tools it
// would in a direct launch, even when the tmux server was started from an
// older shell. `-e` needs tmux 3.0 or newer.
type Tmux struct {
	// NoSwitch leaves the user where they are after the tmux session is
	// created or found, and reports it with a notice instead.
	NoSwitch bool
}

// Name implements Launcher.
func (Tmux) Name() string { return config.LauncherTmux }

// tmuxRun runs a tmux command and returns its standard output. A failure's
// error carries tmux's own message. It is a variable so tests can record the
// commands instead of driving a real tmux server.
var tmuxRun = func(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("tmux %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return string(out), nil
}

// tmuxAttach attaches the current terminal to a tmux session. With replace
// it replaces vmono with `tmux attach-session` where exec is supported, and
// otherwise runs it as a child until the client detaches. It is a variable
// so tests can observe the attach.
var tmuxAttach = func(target string, replace bool) error {
	path, err := lookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found on PATH")
	}
	argv := []string{"tmux", "attach-session", "-t", target}
	if replace {
		if err := execReplace(path, argv); !errors.Is(err, errReplaceUnsupported) {
			return err
		}
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// errReplaceUnsupported tells tmuxAttach to fall back to a child process.
var errReplaceUnsupported = errors.New("exec replacement unsupported")

// Launch implements Launcher. See Tmux for the steps.
func (t Tmux) Launch(l *harness.Launch, opts Opts) (Result, error) {
	tag := l.Package + "/" + l.Session
	id, err := findTmuxSession(tag)
	if err != nil {
		return HandedOff, err
	}
	if id == "" {
		if id, err = createTmuxSession(l, tag); err != nil {
			return HandedOff, err
		}
	}
	inside := os.Getenv("TMUX") != ""
	if t.NoSwitch {
		notifyTmux(l, tag, inside)
		return Elsewhere, nil
	}
	if inside {
		if _, err := tmuxRun("switch-client", "-t", id); err != nil {
			return HandedOff, err
		}
		return Elsewhere, nil
	}
	return HandedOff, tmuxAttach(id, opts.Replace)
}

// Find implements Closer. The tmux session must carry the tag and have been
// started at dir, so one that belongs to a package of the same name under
// another package root is left alone.
func (Tmux) Find(tag, dir string) (*Place, error) {
	out, err := tmuxRun("list-sessions", "-F", "#{session_id}\t#{"+TagOption+"}\t#{session_path}")
	if err != nil {
		return nil, nil // no server, so nothing is running
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(f) != 3 || f[1] != tag || !samePath(f[2], dir) {
			continue
		}
		inside := false
		if os.Getenv("TMUX") != "" {
			here, err := tmuxRun("display-message", "-p", "#{session_id}")
			if err != nil {
				return nil, err
			}
			inside = strings.TrimSpace(here) == f[0]
		}
		return &Place{Name: fmt.Sprintf("tmux session %q", TmuxSessionName(tag)), Inside: inside, Close: func() error {
			if inside {
				leaveTmuxSession()
			}
			_, err := tmuxRun("kill-session", "-t", f[0])
			return err
		}}, nil
	}
	return nil, nil
}

// leaveTmuxSession switches the client vmono runs in to the tmux session it was
// in before, or else to the next one, since tmux detaches a client whose session
// is killed. With no other session there is nowhere to go.
func leaveTmuxSession() {
	if _, err := tmuxRun("switch-client", "-l"); err != nil {
		_, _ = tmuxRun("switch-client", "-n")
	}
}

// notifyTmux tells the user which tmux session the harness runs in when
// they were not taken there. Inside tmux it also shows the message in the
// client's status line, best effort: a failure to show it is not a failure
// to launch. The standard error copy names how to get there from where the
// user is, which differs inside and outside tmux.
func notifyTmux(l *harness.Launch, tag string, inside bool) {
	name := TmuxSessionName(tag)
	if inside {
		msg := fmt.Sprintf("vmono: %s is running in tmux session %q (prefix + s to switch)", l.Name, name)
		_, _ = tmuxRun("display-message", msg)
		fmt.Fprintln(os.Stderr, msg)
		return
	}
	fmt.Fprintf(os.Stderr, "vmono: %s is running in tmux session %q; attach with: tmux attach -t %s\n", l.Name, name, name)
}

// findTmuxSession returns the id (such as "$3") of the tmux session tagged
// with tag, or "" when there is none. A tmux with no server running yet
// fails to list sessions; that, like any other listing failure, is treated
// as having none, and a real problem with tmux surfaces when the session is
// created.
func findTmuxSession(tag string) (string, error) {
	out, err := tmuxRun("list-sessions", "-F", "#{session_id}\t#{"+TagOption+"}")
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(out, "\n") {
		id, got, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if ok && got == tag {
			return id, nil
		}
	}
	return "", nil
}

// createTmuxSession starts the harness in a new detached tmux session at the
// session root, tags it, and returns its id. The id is used as the target
// from here on, so nothing depends on how tmux matches names.
func createTmuxSession(l *harness.Launch, tag string) (string, error) {
	args := []string{"new-session", "-d", "-P", "-F", "#{session_id}", "-s", TmuxSessionName(tag), "-c", l.Dir}
	for _, kv := range tmuxEnv(l) {
		args = append(args, "-e", kv)
	}
	args = append(args, l.Path)
	args = append(args, l.Args...)
	out, err := tmuxRun(args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if _, err := tmuxRun("set-option", "-t", id, TagOption, tag); err != nil {
		again := "open " + tag
		if l.Self != nil {
			again = shellLine(l.Self)
		}
		return "", fmt.Errorf("tmux session for %s ended as soon as it started, so the harness probably exited at once; "+
			"run `VMONO_LAUNCHER=direct vmono %s` to see why (%w)", tag, again, err)
	}
	return id, nil
}

// tmuxEnv returns the KEY=VALUE pairs passed to new-session: vmono's
// overrides and the current PATH, sorted so the command is stable.
func tmuxEnv(l *harness.Launch) []string {
	env := map[string]string{}
	if p, ok := os.LookupEnv("PATH"); ok {
		env["PATH"] = p
	}
	for k, v := range l.Overrides {
		env[k] = v
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// TmuxSessionName turns "pkg/session" into a valid tmux session name by
// replacing the characters tmux reserves in names, '.' and ':', with '_'.
func TmuxSessionName(tag string) string {
	return strings.NewReplacer(".", "_", ":", "_").Replace(tag)
}
