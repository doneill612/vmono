package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
)

// Closer is a launcher that gives a session a place of its own, which has to
// be closed when the session is removed.
type Closer interface {
	Launcher
	// Find looks for the place the session "pkg/session" at dir runs in and
	// returns its name and how to close it, killing the harness in it. close
	// is nil when there is none. The error is errInside when vmono is known to
	// run there itself.
	Find(tag, dir string) (place string, close func() error, err error)
}

// errInside is returned for the place vmono itself runs in. Closing it would
// take away the terminal the user is reading vmono's result in.
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
//
// The two are separate because finding needs the session's directory to
// exist, while closing has to come after everything else. Not every terminal
// tells vmono where it is (a herdr popup does not), so closing one can kill
// vmono. A place that cannot be closed never fails the removal.
func FindSession(pkg, session, dir string) func() []string {
	var notes []string
	var found []func() string
	for _, c := range closers() {
		place, closePlace, err := c.Find(pkg+"/"+session, dir)
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("left its %s open: %v", c.Name(), err))
		case closePlace != nil:
			found = append(found, func() string {
				if err := closePlace(); err != nil {
					return fmt.Sprintf("left its %s open: %v", place, err)
				}
				return "closed " + place
			})
		}
	}
	if len(notes)+len(found) == 0 {
		return nil
	}
	return func() []string {
		for _, f := range found {
			notes = append(notes, f())
		}
		return notes
	}
}

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
