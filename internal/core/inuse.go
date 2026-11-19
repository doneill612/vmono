package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// cwdBlocksRemoval reports whether the operating system refuses to delete a
// directory that is some process's working directory. Windows does, and so
// git's `worktree remove` fails there with a bare "Permission denied" after
// vmono has already done part of its work; Unix removes the directory without
// complaint. It is a variable so tests can turn the check on everywhere.
var cwdBlocksRemoval = runtime.GOOS == "windows"

// InUseError reports that a directory vmono was asked to remove contains the
// working directory. It is not an ErrRefused: no work would be lost and
// --force cannot help, since the directory is in use however it is asked to
// go. It maps to the generic exit code.
type InUseError struct {
	// Dir is the directory that could not be removed.
	Dir string
	// Cwd is the working directory found inside it.
	Cwd string
}

// Error implements error.
func (e *InUseError) Error() string {
	return fmt.Sprintf("cannot remove %s: the current directory %s is inside it, and Windows cannot delete a directory in use; cd out of it and run the command again, naming the session explicitly", e.Dir, e.Cwd)
}

// checkNotInside returns an *InUseError when the working directory is, or is
// inside, any of dirs, and nil otherwise. It does nothing where
// cwdBlocksRemoval is false.
//
// Directories are compared with os.SameFile on each ancestor of the working
// directory rather than as text, because the same directory has several
// spellings on Windows: 8.3 short names (RUNNER~1), mixed case and slash
// direction. The check is advisory, so anything it cannot determine (no
// working directory, a target that does not exist) lets the operation
// proceed; it must never block a removal that would have worked.
func checkNotInside(dirs ...string) error {
	if !cwdBlocksRemoval {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	for _, dir := range dirs {
		target, err := os.Stat(dir)
		if err != nil {
			continue
		}
		for at := cwd; ; {
			if info, err := os.Stat(at); err == nil && os.SameFile(info, target) {
				return &InUseError{Dir: dir, Cwd: cwd}
			}
			parent := filepath.Dir(at)
			if parent == at {
				break
			}
			at = parent
		}
	}
	return nil
}
