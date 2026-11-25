package harness

import (
	"os"
	"os/signal"
	"time"
)

// stopGrace is how long a harness that was told to stop has to do so.
const stopGrace = 5 * time.Second

// RunAndReturn runs the harness as a child and always comes back, so that the
// caller can clean up after it. An interrupt is the harness's to handle and
// does not end vmono. When the terminal goes away or vmono is told to stop,
// the harness is told the same, and killed if it does not listen. A non-zero
// exit comes back as an *ExitError.
func RunAndReturn(l *Launch, _ bool) error {
	// Notify, not Ignore: a child inherits ignored signals.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, append([]os.Signal{os.Interrupt}, stopSignals...)...)
	defer signal.Stop(sigs)

	cmd := command(l)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var kill <-chan time.Time
	for {
		select {
		case err := <-done:
			return exitError(err)
		case sig := <-sigs:
			if sig != os.Interrupt {
				_ = cmd.Process.Signal(sig)
				if kill == nil {
					kill = time.After(stopGrace)
				}
			}
		case <-kill:
			_ = cmd.Process.Kill()
		}
	}
}
