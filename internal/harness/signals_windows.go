//go:build windows

package harness

import (
	"os"
	"syscall"
)

// stopSignals are what ends vmono, and so the harness it waits for. Windows
// reports a closing console as a SIGTERM.
var stopSignals = []os.Signal{syscall.SIGTERM}
