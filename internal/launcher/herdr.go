package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/harness"
	"github.com/doneill612/vmono/internal/herdr"
)

// Herdr runs each vmono session's harness in a herdr workspace of its own,
// labeled "pkg/session" and rooted at the session.
//
// The workspace is found again by a metadata token, or, since a herdr restart
// drops those, by its label together with a pane at the session root, and is
// then marked again. Launching:
//
//  1. creates the workspace if there is none, or takes the one there is;
//  2. starts the harness in a pane of it whose shell is at its prompt, unless
//     an agent is already in the workspace or every pane is busy, so there is
//     never a second harness;
//  3. focuses the workspace.
//
// The harness is started by typing `vmono open` into the shell, with the
// direct launcher. That keeps the line short, since herdr limits its length,
// keeps a harness's configured environment off the screen, and leaves the
// shell there when the harness exits.
//
// The workspace is not focused with NoFocus, or when an agent asked for the
// launch, since neither should move the user: the command that does is printed
// instead. vmono's own terminal is never given up, so the result is always
// Elsewhere.
type Herdr struct {
	// NoFocus leaves the user where they are.
	NoFocus bool
}

// Name implements Launcher.
func (Herdr) Name() string { return config.LauncherHerdr }

// selfPath returns the vmono binary to run in the workspace. It is a variable
// because in tests the executable is the test binary.
var selfPath = os.Executable

// Launch implements Launcher. See Herdr for the steps.
func (h Herdr) Launch(l *harness.Launch, _ Opts) (Result, error) {
	tag := l.Package + "/" + l.Session
	snap, err := herdr.TakeSnapshot()
	if herdr.IsCode(err, herdr.CodeNotRunning) {
		return HandedOff, fmt.Errorf("herdr is not running; start it with `herdr`, or use VMONO_LAUNCHER=direct")
	}
	if err != nil {
		return HandedOff, err
	}
	id, pane := findHerdrWorkspace(snap, tag, l.Dir), ""
	if id == "" {
		if id, pane, err = herdr.CreateWorkspace(l.Dir, tag); err != nil {
			return HandedOff, err
		}
	} else if pane, err = freePane(snap, id); err != nil {
		return HandedOff, err
	}
	if err := herdr.Tag(id, herdrToken(tag, l.Dir)); err != nil {
		return HandedOff, err
	}
	if pane != "" {
		line, err := openLine(l, tag)
		if err != nil {
			return HandedOff, err
		}
		if err := herdr.RunIn(pane, line); err != nil {
			return HandedOff, err
		}
	}
	if h.NoFocus || byAgent() {
		fmt.Fprintf(os.Stderr, "vmono: %s is running in herdr workspace %q; show it with: herdr workspace focus %s\n", l.Name, tag, id)
		return Elsewhere, nil
	}
	if err := herdr.Focus(id); err != nil {
		return HandedOff, err
	}
	if !herdr.Inside() {
		fmt.Fprintf(os.Stderr, "vmono: %s is running in herdr workspace %q; run `herdr` to see it\n", l.Name, tag)
	}
	return Elsewhere, nil
}

// findHerdrWorkspace returns the id of the session's workspace, or "". Unless
// the token says it outright, it must have a pane at or below the session root: a
// workspace is closed along with the session, so one of the user's own must never
// be mistaken for it. That goes for the label, and for a token from before it
// held the directory, when it was the tag alone.
func findHerdrWorkspace(snap herdr.Snapshot, tag, dir string) string {
	for _, w := range snap.Workspaces {
		if w.Tokens[herdr.Token] == herdrToken(tag, dir) {
			return w.ID
		}
	}
	for _, w := range snap.Workspaces {
		if token := w.Tokens[herdr.Token]; (token == tag || token == "" && w.Label == tag) && hasPaneIn(snap, w.ID, dir) {
			return w.ID
		}
	}
	return ""
}

// herdrToken is the value of the token that marks the workspace of the session
// tag at dir: the tag, and a short hash of the directory, which tells packages of
// the same name under different package roots apart wherever the workspace's
// shells have gone since.
func herdrToken(tag, dir string) string {
	sum := sha256.Sum256([]byte(resolvePath(dir)))
	return tag + "/" + hex.EncodeToString(sum[:4])
}

// hasPaneIn reports whether a workspace has a pane at or below dir.
func hasPaneIn(snap herdr.Snapshot, id, dir string) bool {
	for _, p := range snap.Panes {
		if p.WorkspaceID == id && (within(p.Cwd, dir) || within(p.ForegroundCwd, dir)) {
			return true
		}
	}
	return false
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(resolvePath(dir), resolvePath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// freePane returns a pane of the workspace to start a harness in, or "" when
// an agent is already there or no shell is at its prompt.
func freePane(snap herdr.Snapshot, id string) (string, error) {
	for _, a := range snap.Agents {
		if a.WorkspaceID == id {
			return "", nil
		}
	}
	for _, p := range snap.Panes {
		if p.WorkspaceID != id {
			continue
		}
		at, err := herdr.AtPrompt(p.ID)
		if err != nil {
			return "", err
		}
		if at {
			return p.ID, nil
		}
	}
	return "", nil
}

// openLine builds the command line that opens the session in place. The
// VMONO_* settings vmono runs with go along, since the shell belongs to the
// herdr server's environment, except the ones that identify a session, which
// may be those of the agent asking.
func openLine(l *harness.Launch, tag string) (string, error) {
	self, err := selfPath()
	if err != nil {
		return "", err
	}
	argv := append([]string{"env", "VMONO_LAUNCHER=" + config.LauncherDirect}, settingsEnv()...)
	argv = append(argv, self)
	if l.Self != nil {
		return shellLine(append(argv, l.Self...)), nil
	}
	argv = append(argv, "open", tag, "--harness", l.Name)
	if extra := l.Args[min(len(l.Harness.Args), len(l.Args)):]; len(extra) > 0 {
		argv = append(append(argv, "--"), extra...)
	}
	return shellLine(argv), nil
}

// settingsEnv returns the VMONO_* settings vmono runs with, sorted, leaving
// out the ones that identify a session, its author or a launcher.
func settingsEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key, val, _ := strings.Cut(kv, "=")
		switch key {
		case "VMONO_PACKAGE", "VMONO_SESSION", "VMONO_ROOT", "VMONO_BRANCH", "VMONO_AUTHOR", "VMONO_LAUNCHER":
			continue
		}
		if strings.HasPrefix(key, "VMONO_") && val != "" {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	return env
}

// shellLine quotes and joins a command line.
func shellLine(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote quotes an argument for sh, zsh, bash and fish alike.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./=:,+@") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Find implements Closer.
func (Herdr) Find(tag, dir string) (*Place, error) {
	snap, err := herdr.TakeSnapshot()
	if herdr.IsCode(err, herdr.CodeNotRunning) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	id := findHerdrWorkspace(snap, tag, dir)
	if id == "" {
		return nil, nil
	}
	return &Place{
		Name:   fmt.Sprintf("herdr workspace %q", tag),
		Inside: id == herdr.CurrentWorkspace(),
		Close:  func() error { return herdr.Close(id) },
	}, nil
}
