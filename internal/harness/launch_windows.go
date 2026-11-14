//go:build windows

package harness

// execReplace is unsupported on Windows, which has no exec(2); Run falls
// back to running the harness as a child process.
func execReplace(l *Launch) error { return errReplaceUnsupported }
