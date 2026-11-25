package launcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/herdr"
)

// Closer is a launcher that gives a session a place of its own, which has to
// be closed when the session is removed.
type Closer interface {
	Launcher
	// Find looks for the place the session "pkg/session" at dir runs in. It is
	// nil when there is none.
	Find(tag, dir string) (*Place, error)
}

// Place is where a session runs, such as a tmux session.
type Place struct {
	// Name is what to call it in messages.
	Name string
	// Inside is true when vmono is known to run in it, or in a popup over it.
	// Closing it may then end vmono, so it comes last.
	Inside bool
	// Close closes it, killing the harness in it.
	Close func() error
}

// errInside is why a place an agent runs in is left open when it is the agent
// that removes the session: closing it would kill the agent before it could say
// what it did.
var errInside = errors.New("vmono is running inside it")

// closers returns every launcher that may have a place to close and can run
// on this machine. It does not matter which one is configured now: the
// session may have been opened under another.
func closers() []Closer {
	var out []Closer
	if _, err := lookPath("tmux"); err == nil {
		out = append(out, Tmux{})
	}
	if runtime.GOOS != "windows" {
		if _, err := lookPath("herdr"); err == nil {
			out = append(out, Herdr{})
		}
	}
	return out
}

// FindSession finds wherever the session is running, under any launcher, and
// returns a function that closes it and reports what it closed or had to leave
// open, one line each. It is nil when there is nothing to close or report.
// inside is true when vmono itself runs there, so that nothing can be counted on
// to happen after the function is called.
//
// The two are separate because finding needs the session's directory to
// exist, while closing has to come after everything else. A place that cannot be
// closed never fails the removal. One an agent runs in is left open when the
// agent is the one asking.
func FindSession(pkg, session, dir string) (closeIt func() []string, inside bool) {
	var notes []string
	var found []*Place
	for _, c := range closers() {
		place, err := c.Find(pkg+"/"+session, dir)
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("left its %s open: %v", c.Name(), err))
		case place == nil:
		case place.Inside && byAgent():
			notes = append(notes, fmt.Sprintf("left its %s open: %v", place.Name, errInside))
		default:
			found = append(found, place)
			inside = inside || place.Inside
		}
	}
	if len(notes)+len(found) == 0 {
		return nil, false
	}
	slices.SortStableFunc(found, func(a, b *Place) int {
		switch {
		case a.Inside == b.Inside:
			return 0
		case b.Inside:
			return -1
		}
		return 1
	})
	return func() []string {
		for _, place := range found {
			if err := place.Close(); err != nil {
				notes = append(notes, fmt.Sprintf("left its %s open: %v", place.Name, err))
			} else {
				notes = append(notes, "closed "+place.Name)
			}
		}
		return notes
	}, inside
}

// InsideOf returns the name of the place the session runs in when vmono runs
// there too, so that removing the session closes it around the user, and ""
// otherwise.
func InsideOf(pkg, session, dir string) string {
	if os.Getenv("TMUX") == "" && !herdr.Inside() || byAgent() {
		return ""
	}
	for _, c := range closers() {
		if place, err := c.Find(pkg+"/"+session, dir); err == nil && place != nil && place.Inside {
			return place.Name
		}
	}
	return ""
}

// byAgent reports whether an agent is running vmono.
func byAgent() bool { return strings.HasPrefix(os.Getenv("VMONO_AUTHOR"), "agent:") }

// samePath reports whether two paths are the same directory, through symlinks
// where they can be resolved.
func samePath(a, b string) bool { return resolvePath(a) == resolvePath(b) }

// resolvePath cleans a path and resolves the symlinks in as much of it as
// exists, so that a directory that is already gone still compares equal.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(resolvePath(parent), filepath.Base(p))
}
