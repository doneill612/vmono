package core

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/hooks"
	"github.com/doneill612/vmono/internal/manifest"
)

// SessionEnv returns the variables that identify a session to what vmono runs
// in it, harnesses and hooks alike.
func SessionEnv(s *Session) map[string]string {
	return map[string]string{
		"VMONO_PACKAGE": s.Pkg.Name,
		"VMONO_SESSION": s.Name,
		"VMONO_ROOT":    s.Dir,
		"VMONO_BRANCH":  s.S.Branch,
	}
}

// memberHookSteps returns the step that runs a member's setup or teardown
// commands in its worktree, or nothing when it has none or hooks are off. The commands run in
// order. One that fails skips the rest and is reported as a warning: a broken
// install should not cost the user the session, nor keep one from being
// removed. Only an interrupt fails the step.
func (c *Core) memberHookSteps(s *Session, mem manifest.Member, src, name string, cmds manifest.Commands) []apply.Step {
	if len(cmds) == 0 || c.Cfg.NoHooks {
		return nil
	}
	dir := s.MemberDir(mem)
	return []apply.Step{{Member: mem.Name, Name: name, RunOut: func(warn, out func(string)) error {
		if _, err := os.Stat(dir); err != nil {
			return nil
		}
		env := SessionEnv(s)
		env["VMONO_MEMBER"], env["VMONO_MEMBER_DIR"], env["VMONO_SOURCE"] = mem.Name, dir, src
		env["VMONO_BRANCH"] = s.BranchFor(mem.Name)
		for _, cmd := range cmds {
			err := hooks.Run(hooks.Spec{Command: cmd, Dir: dir, Env: env}, out)
			if errors.Is(err, hooks.ErrInterrupted) {
				return err
			}
			if err != nil {
				warn("failed, continuing without it: " + err.Error())
				return nil
			}
		}
		return nil
	}}}
}

// hookSteps returns the step that runs the named global hook for a session, in
// the session's directory, or nothing when it is not configured or hooks are
// off. extra is set on top of the session's variables. A failing command only
// warns, unless the hook says to abort.
func (c *Core) hookSteps(name string, s *Session, extra map[string]string) []apply.Step {
	h, ok := c.Cfg.Hooks[name]
	if !ok || c.Cfg.NoHooks {
		return nil
	}
	return []apply.Step{{Name: name + " hook", RunOut: func(warn, out func(string)) error {
		env := SessionEnv(s)
		env["VMONO_HOOK"] = name
		maps.Copy(env, extra)
		err := hooks.Run(hooks.Spec{Command: h.Command, Dir: s.Dir, Env: env, Timeout: h.Limit()}, out)
		if err == nil || errors.Is(err, hooks.ErrInterrupted) || h.OnFailure == config.OnFailureAbort {
			return err
		}
		warn("failed, continuing without it: " + err.Error())
		return nil
	}}}
}

// HookVetoError is returned when a pre_remove hook set to abort fails, so a
// session was not removed. Nothing has been removed when it is returned. It
// matches ErrRefused, so the CLI exits with 2.
type HookVetoError struct {
	Session string
	Err     error
}

// Error implements error.
func (e *HookVetoError) Error() string {
	return fmt.Sprintf("refused: the %s hook stopped %s from being removed: %v (--no-hooks skips it; --force does not)", config.HookPreRemove, e.Session, e.Err)
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *HookVetoError) Is(target error) bool { return target == ErrRefused }

// Unwrap returns what went wrong with the hook.
func (e *HookVetoError) Unwrap() error { return e.Err }

// preRemove runs the pre_remove hook of each session about to be removed. It
// runs for all of them before anything goes, so one veto keeps them all.
func (c *Core) preRemove(sessions ...*Session) error {
	for _, s := range sessions {
		steps := c.hookSteps(config.HookPreRemove, s, nil)
		if len(sessions) > 1 {
			labelSteps(steps, s.Name)
		}
		err := (&apply.Runner{Events: c.Events}).Run(steps)
		var step *apply.StepError
		if errors.As(err, &step) {
			return &HookVetoError{Session: s.Label(), Err: step.Err}
		}
	}
	return nil
}
