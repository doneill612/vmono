// Package apply runs a list of steps one at a time, reporting progress as
// events and undoing completed steps when one fails. The TUI's progress
// screen and the CLI's line output both read the same event stream, so they
// always report the same steps.
package apply

import (
	"errors"
	"fmt"
	"strings"
)

// Status is the state of a step as reported in an Event.
type Status string

// Step statuses.
const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusWarn    Status = "warn"
	StatusUndone  Status = "undone"
)

// Event reports progress on one step of one member. Member is empty for
// package-level steps.
type Event struct {
	Member  string
	Step    string
	Status  Status
	Message string
	Err     error
}

// Step is one unit of work. Undo, when set, reverses Run and is called in
// reverse order for completed steps when a later step fails and the runner
// has Rollback enabled.
type Step struct {
	Member string
	Name   string
	// Run does the work. It may call warn to report a non-fatal problem.
	Run  func(warn func(msg string)) error
	Undo func() error
}

// Runner executes steps. Events may be nil. The runner never closes the
// channel; the caller owns it and must drain it.
type Runner struct {
	Events chan<- Event
	// Rollback undoes completed steps, newest first, when a step fails.
	Rollback bool
}

// StepError is returned when a step fails. It names the member so callers
// can report which one broke.
type StepError struct {
	Member string
	Step   string
	Err    error
	// UndoErrs holds errors from rolling back, if any occurred.
	UndoErrs []error
}

// Error implements error.
func (e *StepError) Error() string {
	var b strings.Builder
	if e.Member != "" {
		fmt.Fprintf(&b, "%s: ", e.Member)
	}
	fmt.Fprintf(&b, "%s: %v", e.Step, e.Err)
	if len(e.UndoErrs) > 0 {
		fmt.Fprintf(&b, " (rollback incomplete: %v)", errors.Join(e.UndoErrs...))
	}
	return b.String()
}

// Unwrap returns the step's error.
func (e *StepError) Unwrap() error { return e.Err }

// emit sends an event if a channel is attached.
func (r *Runner) emit(ev Event) {
	if r.Events != nil {
		r.Events <- ev
	}
}

// Run executes steps in order. On the first failure it emits a failed
// event, optionally rolls back, and returns a *StepError.
func (r *Runner) Run(steps []Step) error {
	var done []Step
	for _, s := range steps {
		r.emit(Event{Member: s.Member, Step: s.Name, Status: StatusRunning})
		warn := func(msg string) {
			r.emit(Event{Member: s.Member, Step: s.Name, Status: StatusWarn, Message: msg})
		}
		if err := s.Run(warn); err != nil {
			r.emit(Event{Member: s.Member, Step: s.Name, Status: StatusFailed, Err: err})
			se := &StepError{Member: s.Member, Step: s.Name, Err: err}
			if r.Rollback {
				se.UndoErrs = r.undo(done)
			}
			return se
		}
		r.emit(Event{Member: s.Member, Step: s.Name, Status: StatusDone})
		done = append(done, s)
	}
	return nil
}

// undo reverses completed steps newest first, collecting errors rather than
// stopping so as much as possible is cleaned up.
func (r *Runner) undo(done []Step) []error {
	var errs []error
	for i := len(done) - 1; i >= 0; i-- {
		s := done[i]
		if s.Undo == nil {
			continue
		}
		if err := s.Undo(); err != nil {
			errs = append(errs, fmt.Errorf("undo %s %s: %w", s.Member, s.Name, err))
			continue
		}
		r.emit(Event{Member: s.Member, Step: s.Name, Status: StatusUndone})
	}
	return errs
}
