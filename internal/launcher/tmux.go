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
type Tmux struct{}

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
func (Tmux) Launch(l *harness.Launch, opts Opts) (Result, error) {
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
	if os.Getenv("TMUX") != "" {
		if _, err := tmuxRun("switch-client", "-t", id); err != nil {
			return HandedOff, err
		}
		return Elsewhere, nil
	}
	return HandedOff, tmuxAttach(id, opts.Replace)
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
		return "", fmt.Errorf("tmux session for %s ended as soon as it started, so the harness probably exited at once; "+
			"run `VMONO_LAUNCHER=direct vmono open %s` to see why (%w)", tag, tag, err)
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
