//go:build !windows

package launcher

import "syscall"

// execReplace replaces vmono with the program at path. On success it does
// not return.
func execReplace(path string, argv []string) error {
	return syscall.Exec(path, argv, syscall.Environ())
}
