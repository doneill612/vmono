//go:build !windows

package harness

import (
	"os"
	"syscall"
)

// execReplace replaces the current process with the harness. On success it
// does not return. The working directory is changed first because exec
// inherits it.
func execReplace(l *Launch) error {
	if err := os.Chdir(l.Dir); err != nil {
		return err
	}
	argv := append([]string{l.Path}, l.Args...)
	return syscall.Exec(l.Path, argv, l.Env)
}
