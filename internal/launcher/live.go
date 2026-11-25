package launcher

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/herdr"
)

// Running is the state of a harness that is running but whose agent's state
// is not known, such as one in tmux. The others are herdr's.
const Running = "running"

// urgency orders states by how much they want the user's attention.
var urgency = map[string]int{Running: 1, herdr.Idle: 2, herdr.Done: 3, herdr.Working: 4, herdr.Blocked: 5}

// Urgency returns how much a state wants the user's attention, more the
// higher, and 0 for a session with nothing running.
func Urgency(state string) int { return urgency[state] }

// Live is what is running where, as of one look at the launchers.
type Live struct{ agents []liveAgent }

// liveAgent is one harness, at a resolved directory.
type liveAgent struct{ dir, state string }

// HasLive reports whether the configured launcher gives sessions places that
// can be looked at.
func HasLive(cfg config.Config) bool {
	l, err := Resolve(cfg)
	if err != nil {
		return false
	}
	_, ok := l.(Closer)
	return ok
}

// TakeLive looks at every launcher that can run on this machine, since a
// session may have been opened under any of them. One whose server is not
// running has nothing to show. Any other failure is returned with what could
// be seen.
func TakeLive() (Live, error) {
	var l Live
	var failed error
	if _, err := lookPath("tmux"); err == nil {
		// A failure to list means no server.
		out, _ := tmuxRun("list-sessions", "-F", "#{"+TagOption+"}\t#{session_path}")
		for _, line := range strings.Split(out, "\n") {
			tag, dir, _ := strings.Cut(strings.TrimRight(line, "\r"), "\t")
			if tag != "" && dir != "" {
				l.agents = append(l.agents, liveAgent{resolvePath(dir), Running})
			}
		}
	}
	if _, err := lookPath("herdr"); err == nil && runtime.GOOS != "windows" {
		snap, err := herdr.TakeSnapshot()
		if err != nil && !herdr.IsCode(err, herdr.CodeNotRunning) {
			failed = err
		}
		for _, a := range snap.Agents {
			dir, state := a.ForegroundCwd, a.Status
			if dir == "" {
				dir = a.Cwd
			}
			if dir == "" {
				continue
			}
			if urgency[state] == 0 {
				state = Running
			}
			l.agents = append(l.agents, liveAgent{resolvePath(dir), state})
		}
	}
	return l, failed
}

// Sessions returns the state of each session of the package at dir that has a
// harness running in it, by name. A session with several shows the one that
// wants attention most.
func (l Live) Sessions(dir string) map[string]string {
	out := map[string]string{}
	root := resolvePath(dir)
	for _, a := range l.agents {
		rel, err := filepath.Rel(root, a.dir)
		if err != nil {
			continue
		}
		name, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if strings.HasPrefix(name, ".") { // the package itself, outside it, or its init workspace
			continue
		}
		if urgency[a.state] > urgency[out[name]] {
			out[name] = a.state
		}
	}
	return out
}
