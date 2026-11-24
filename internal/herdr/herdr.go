// Package herdr drives herdr, a terminal workspace manager for coding agents,
// through its command line.
//
// What vmono relies on was checked against herdr 0.9.5 (API protocol 22), and
// the files in testdata are its output:
//
//   - Control commands print a JSON envelope: a "result" on standard output,
//     or an "error" with a code on standard error and exit status 1.
//   - A workspace always starts as a shell at its cwd. `pane run` types a
//     command line into that shell, limited on macOS to about 1000 bytes.
//   - `workspace report-metadata` with no TTL attaches tokens that survive a
//     rename and come back in `workspace list` and `api snapshot`, but not a
//     server restart, after which the workspace still has its label and cwd.
//   - Panes get HERDR_ENV=1 and the ids of where they are. HERDR_SOCKET_PATH
//     and HERDR_SESSION make the CLI talk to the server they belong to.
package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Token is the metadata token vmono sets on the workspaces it creates, holding
// the session they belong to as "pkg/session".
const Token = "vmono_session"

// Agent states, as herdr reports them.
const (
	Working = "working"
	Blocked = "blocked"
	Idle    = "idle"
	Done    = "done"
	Unknown = "unknown"
)

// CodeNotRunning is the code of the Error for a server that is not running.
const CodeNotRunning = "server_not_running"

// Workspace is one herdr workspace.
type Workspace struct {
	ID     string            `json:"workspace_id"`
	Label  string            `json:"label"`
	Tokens map[string]string `json:"tokens"`
	// Status is the state of the agents in it, taken together.
	Status string `json:"agent_status"`
}

// Pane is one terminal in a workspace. Agent is empty when herdr recognizes
// no coding agent in it.
type Pane struct {
	ID            string `json:"pane_id"`
	WorkspaceID   string `json:"workspace_id"`
	Cwd           string `json:"cwd"`
	ForegroundCwd string `json:"foreground_cwd"`
	Agent         string `json:"agent"`
	Status        string `json:"agent_status"`
}

// Snapshot is everything the server is running.
type Snapshot struct {
	Workspaces []Workspace `json:"workspaces"`
	Panes      []Pane      `json:"panes"`
	Agents     []Pane      `json:"agents"`
}

// Error is an error herdr reported.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements error.
func (e *Error) Error() string { return "herdr: " + e.Message }

// IsCode reports whether err is a herdr error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Run runs a herdr command and returns its standard output. It is a variable
// so tests can record commands instead of driving a real server.
var Run = func(args ...string) ([]byte, error) {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	if e := parseError(stderr.Bytes()); e != nil {
		return nil, e
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		return nil, fmt.Errorf("herdr %s: %s", strings.Join(args[:min(2, len(args))], " "), msg)
	}
	return nil, fmt.Errorf("herdr %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
}

// Inside reports whether vmono runs in a herdr pane.
func Inside() bool { return os.Getenv("HERDR_ENV") == "1" }

// CurrentWorkspace returns the id of the workspace vmono runs in, or "".
func CurrentWorkspace() string {
	if !Inside() {
		return ""
	}
	return os.Getenv("HERDR_WORKSPACE_ID")
}

// parseError decodes herdr's error envelope, or returns nil for anything else.
func parseError(data []byte) *Error {
	var env struct {
		Error *Error `json:"error"`
	}
	if json.Unmarshal(bytes.TrimSpace(data), &env) != nil {
		return nil
	}
	return env.Error
}

// call runs a command and decodes its result into out.
func call(out any, args ...string) error {
	data, err := Run(args...)
	if err != nil {
		return err
	}
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil || env.Result == nil {
		return fmt.Errorf("herdr %s: unexpected output: %.80q", strings.Join(args[:min(2, len(args))], " "), data)
	}
	return json.Unmarshal(env.Result, out)
}

// TakeSnapshot returns everything the server is running.
func TakeSnapshot() (Snapshot, error) {
	var r struct{ Snapshot Snapshot }
	err := call(&r, "api", "snapshot")
	return r.Snapshot, err
}

// CreateWorkspace creates a workspace without focusing it and returns its id
// and that of its one pane, a shell at cwd.
func CreateWorkspace(cwd, label string) (id, pane string, err error) {
	var r struct {
		Workspace Workspace `json:"workspace"`
		RootPane  Pane      `json:"root_pane"`
	}
	if err := call(&r, "workspace", "create", "--cwd", cwd, "--label", label, "--no-focus"); err != nil {
		return "", "", err
	}
	if r.Workspace.ID == "" || r.RootPane.ID == "" {
		return "", "", errors.New("herdr workspace create: no workspace in its output")
	}
	return r.Workspace.ID, r.RootPane.ID, nil
}

// Tag marks a workspace as belonging to the session "pkg/session".
func Tag(id, session string) error {
	_, err := Run("workspace", "report-metadata", id, "--source", "vmono", "--token", Token+"="+session)
	return err
}

// RunIn types a command line into a pane's shell.
func RunIn(pane, line string) error {
	_, err := Run("pane", "run", pane, line)
	return err
}

// AtPrompt reports whether a pane's shell is in the foreground, which is when
// a command can be typed into it.
func AtPrompt(pane string) (bool, error) {
	var r struct {
		Info struct {
			Foreground int `json:"foreground_process_group_id"`
			Shell      int `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := call(&r, "pane", "process-info", "--pane", pane); err != nil {
		return false, err
	}
	return r.Info.Shell != 0 && r.Info.Foreground == r.Info.Shell, nil
}

// Focus shows a workspace in the clients attached to the server.
func Focus(id string) error {
	_, err := Run("workspace", "focus", id)
	return err
}

// Close closes a workspace and what runs in it. Workspaces grouped under it
// are never taken along: herdr refuses instead.
func Close(id string) error {
	_, err := Run("workspace", "close", id)
	return err
}
