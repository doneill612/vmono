package core

import (
	"errors"
	"os"

	"github.com/doneill612/vmono/internal/apply"
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
// commands in its worktree, or nothing when it has none. The commands run in
// order. One that fails skips the rest and is reported as a warning: a broken
// install should not cost the user the session, nor keep one from being
// removed. Only an interrupt fails the step.
func memberHookSteps(s *Session, mem manifest.Member, src, name string, cmds manifest.Commands) []apply.Step {
	if len(cmds) == 0 {
		return nil
	}
	dir := s.MemberDir(mem)
	return []apply.Step{{Member: mem.Name, Name: name, RunOut: func(warn, out func(string)) error {
		if _, err := os.Stat(dir); err != nil {
			return nil
		}
		env := SessionEnv(s)
		env["VMONO_MEMBER"], env["VMONO_MEMBER_DIR"], env["VMONO_SOURCE"] = mem.Name, dir, src
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
