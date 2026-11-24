package launcher

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Closer is a launcher that gives a session a place of its own, which has to
// be closed when the session is removed.
type Closer interface {
	Launcher
	// Close closes the place the session "pkg/session" at dir runs in, if
	// there is one, killing the harness in it. It returns what it closed, ""
	// for nothing, and errInside when vmono itself runs there.
	Close(tag, dir string) (closed string, err error)
}

// errInside is returned instead of closing the place vmono runs in, which
// would kill vmono halfway through removing the session.
var errInside = errors.New("vmono is running inside it")

// closers returns every launcher that may have a place to close and can run
// on this machine. It does not matter which one is configured now: the
// session may have been opened under another.
func closers() []Closer {
	var out []Closer
	if _, err := lookPath("tmux"); err == nil {
		out = append(out, Tmux{})
	}
	return out
}

// CloseSession closes wherever the session is running, under any launcher, and
// reports what it closed or had to leave open, one line each. It never fails:
// a place that cannot be closed should not keep a session from being removed.
func CloseSession(pkg, session, dir string) []string {
	var notes []string
	for _, c := range closers() {
		closed, err := c.Close(pkg+"/"+session, dir)
		switch {
		case err != nil:
			notes = append(notes, fmt.Sprintf("left its %s open: %v", c.Name(), err))
		case closed != "":
			notes = append(notes, "closed "+closed)
		}
	}
	return notes
}

// samePath reports whether two paths are the same directory, through symlinks
// where they can be resolved.
func samePath(a, b string) bool { return resolvePath(a) == resolvePath(b) }

// resolvePath cleans a path and resolves its symlinks when it exists.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
