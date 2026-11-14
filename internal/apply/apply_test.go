package apply

import (
	"errors"
	"testing"
)

// collect drains events produced by fn into a slice.
func collect(fn func(chan<- Event)) []Event {
	ch := make(chan Event, 64)
	fn(ch)
	close(ch)
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestRunSuccess(t *testing.T) {
	var order []string
	steps := []Step{
		{Member: "a", Name: "one", Run: func(func(string)) error { order = append(order, "one"); return nil }},
		{Member: "a", Name: "two", Run: func(warn func(string)) error { warn("careful"); order = append(order, "two"); return nil }},
	}
	var err error
	events := collect(func(ch chan<- Event) { err = (&Runner{Events: ch}).Run(steps) })
	if err != nil || len(order) != 2 {
		t.Fatalf("err=%v order=%v", err, order)
	}
	var warns int
	for _, ev := range events {
		if ev.Status == StatusWarn && ev.Message == "careful" {
			warns++
		}
	}
	if warns != 1 || events[len(events)-1].Status != StatusDone {
		t.Errorf("unexpected events: %+v", events)
	}
}

func TestRollbackReverseOrder(t *testing.T) {
	var undone []string
	mk := func(name string, fail bool) Step {
		return Step{
			Member: name, Name: "step",
			Run: func(func(string)) error {
				if fail {
					return errors.New("boom")
				}
				return nil
			},
			Undo: func() error { undone = append(undone, name); return nil },
		}
	}
	var err error
	events := collect(func(ch chan<- Event) {
		err = (&Runner{Events: ch, Rollback: true}).Run([]Step{mk("a", false), mk("b", false), mk("c", true), mk("d", false)})
	})
	var se *StepError
	if !errors.As(err, &se) || se.Member != "c" {
		t.Fatalf("want StepError for c, got %v", err)
	}
	if len(undone) != 2 || undone[0] != "b" || undone[1] != "a" {
		t.Errorf("undo order = %v, want [b a]", undone)
	}
	var sawFailed, sawUndone bool
	for _, ev := range events {
		sawFailed = sawFailed || ev.Status == StatusFailed
		sawUndone = sawUndone || ev.Status == StatusUndone
	}
	if !sawFailed || !sawUndone {
		t.Errorf("missing failed/undone events: %+v", events)
	}
}

func TestNoRollbackAndNilEvents(t *testing.T) {
	undone := false
	steps := []Step{
		{Name: "a", Run: func(func(string)) error { return nil }, Undo: func() error { undone = true; return nil }},
		{Name: "b", Run: func(func(string)) error { return errors.New("x") }},
	}
	if err := (&Runner{}).Run(steps); err == nil {
		t.Fatal("expected error")
	}
	if undone {
		t.Error("rollback must not run when disabled")
	}
}

func TestUndoErrorsCollected(t *testing.T) {
	steps := []Step{
		{Name: "a", Run: func(func(string)) error { return nil }, Undo: func() error { return errors.New("stuck") }},
		{Name: "b", Run: func(func(string)) error { return errors.New("x") }},
	}
	err := (&Runner{Rollback: true}).Run(steps)
	var se *StepError
	if !errors.As(err, &se) || len(se.UndoErrs) != 1 {
		t.Fatalf("expected one undo error, got %v", err)
	}
}
