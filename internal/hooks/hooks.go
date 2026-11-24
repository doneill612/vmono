// Package hooks runs the shell commands users configure for vmono to run at
// points in a session's life: a member's setup and teardown, and the global
// hooks in config.toml.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout bounds a command that sets no timeout of its own. It is long
// enough for a cold dependency install, and keeps a command that hangs from
// holding up vmono for good.
const DefaultTimeout = 30 * time.Minute

// waitDelay is how long a finished command's output stays open for processes
// it left running in the background, before vmono stops listening.
const waitDelay = 2 * time.Second

// ErrInterrupted is returned when the user interrupted vmono while a command
// ran. Unlike a command's own failure, it should stop the whole operation.
var ErrInterrupted = errors.New("interrupted")

// Spec is one command to run.
type Spec struct {
	// Command is passed to the platform's shell: sh -c, or cmd /c on Windows.
	Command string
	Dir     string
	// Env is set over vmono's own environment.
	Env map[string]string
	// Timeout stops the command and everything it started. Zero means
	// DefaultTimeout.
	Timeout time.Duration
}

// Run runs spec to completion and passes each line it prints, on either
// stream, to out as it is written. It has no standard input, so it cannot
// prompt. A non-zero exit or a timeout is an error naming the command; an
// interrupt matches ErrInterrupted. out is never called after Run returns.
func Run(spec Spec, out func(line string)) error {
	timeout := spec.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// The command gets its own process group so that a timeout reaches all of
	// it, which also takes it out of reach of the terminal's ctrl+c.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := shellCommand(ctx, spec.Command)
	cmd.Dir = spec.Dir
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.WaitDelay = waitDelay
	w := &lineWriter{out: out}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	w.flush()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("`%s`: timed out after %s", spec.Command, timeout)
	case ctx.Err() != nil:
		return fmt.Errorf("`%s`: %w", spec.Command, ErrInterrupted)
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		return nil
	}
	return fmt.Errorf("`%s`: %w", spec.Command, err)
}

// maxLine caps a line held in memory while waiting for its end.
const maxLine = 64 << 10

// lineWriter splits what is written to it into lines. A carriage return ends a
// line too, so a progress bar shows its latest state. Blank lines are dropped.
type lineWriter struct {
	out func(string)
	buf []byte
}

// Write implements io.Writer.
func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexAny(w.buf, "\r\n")
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxLine {
		w.flush()
	}
	return len(p), nil
}

// flush emits an unfinished line.
func (w *lineWriter) flush() {
	w.emit(w.buf)
	w.buf = nil
}

// emit passes one line on.
func (w *lineWriter) emit(line []byte) {
	if s := strings.TrimRight(string(line), " \t"); s != "" && w.out != nil {
		w.out(s)
	}
}
