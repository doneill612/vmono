//go:build windows

package core

import "os"

// processAlive reports whether a process exists. Windows opens the process to
// find it, which fails for one that is gone.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
