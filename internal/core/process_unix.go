//go:build !windows

package core

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process exists. One owned by another user
// does, even though it cannot be signaled.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
