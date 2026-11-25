//go:build !windows

package harness

import (
	"os"
	"syscall"
)

// stopSignals are what ends vmono, and so the harness it waits for.
var stopSignals = []os.Signal{syscall.SIGHUP, syscall.SIGTERM}
