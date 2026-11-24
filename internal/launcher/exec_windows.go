//go:build windows

package launcher

// execReplace is unsupported on Windows, which has no exec; callers fall
// back to running a child process.
func execReplace(string, []string) error { return errReplaceUnsupported }
