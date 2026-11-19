package core

import (
	"fmt"
	"os"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// landingBranch returns the local branch in a member's source repo that the
// session's work lands on: the session's explicit landing branch, else the
// branch named by the member's base, else the source's default branch. The
// second result is true when the session named the branch itself. It is ""
// for a reference member, which has no work of its own to land.
func landingBranch(s *Session, mem manifest.Member, src string) (branch string, explicit bool) {
	if mem.Mode != manifest.ModeActive {
		return "", false
	}
	if branch, explicit = s.S.IntoFor(mem); branch != "" {
		return branch, explicit
	}
	return gitx.DefaultBranch(src), false
}

// MemberSafety is the result of the pre-delete check for one member of one
// session: work that removing its worktree would discard.
type MemberSafety struct {
	// Session names the session the worktree belongs to.
	Session   string `json:"session,omitempty"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Missing   bool   `json:"missing,omitempty"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	// Unpushed counts commits that exist on no remote.
	Unpushed int    `json:"unpushed"`
	Error    string `json:"error,omitempty"`
}

// Label returns "session/member", or just the member when no session is set.
func (s MemberSafety) Label() string {
	if s.Session == "" {
		return s.Name
	}
	return s.Session + "/" + s.Name
}

// Unsafe reports whether removing this member would lose work. A member
// whose state could not be read is treated as unsafe.
func (s MemberSafety) Unsafe() bool {
	return s.Dirty > 0 || s.Untracked > 0 || s.Unpushed > 0 || s.Error != ""
}

// RefusedError is returned when an operation would discard work and force
// was not given. It matches ErrRefused.
type RefusedError struct {
	Unsafe []MemberSafety
}

// Error implements error.
func (e *RefusedError) Error() string {
	var parts []string
	for _, s := range e.Unsafe {
		switch {
		case s.Error != "":
			parts = append(parts, fmt.Sprintf("%s (cannot inspect: %s)", s.Label(), s.Error))
		default:
			parts = append(parts, fmt.Sprintf("%s (%d dirty, %d untracked, %d unpushed)", s.Label(), s.Dirty, s.Untracked, s.Unpushed))
		}
	}
	return ErrRefused.Error() + ": " + strings.Join(parts, "; ") + " (use --force to override)"
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Safety checks the given members of a session for work that would be lost
// if their worktrees were removed: dirty files, untracked files, and commits
// on no remote. Commits that have landed on the member's landing branch in
// its source repo are not counted, since they stay in the repo after the
// worktree goes, which is what finishing a session does. Members whose
// directory is already gone are safe.
func (c *Core) Safety(s *Session, members []manifest.Member) []MemberSafety {
	out := make([]MemberSafety, 0, len(members))
	for _, mem := range members {
		ms := MemberSafety{Session: s.Name, Name: mem.Name, Mode: mem.Mode}
		dir := s.MemberDir(mem)
		if _, err := os.Stat(dir); err != nil {
			ms.Missing = true
			out = append(out, ms)
			continue
		}
		st, err := gitx.StatusOf(dir)
		if err != nil {
			ms.Error = err.Error()
			out = append(out, ms)
			continue
		}
		ms.Dirty, ms.Untracked = st.Dirty, st.Untracked
		var landed string
		if src, err := paths.Abs(mem.Source); err == nil {
			landed, _ = landingBranch(s, mem, src)
		}
		if n, err := gitx.Unpushed(dir, landed); err != nil {
			ms.Error = err.Error()
		} else {
			ms.Unpushed = n
		}
		out = append(out, ms)
	}
	return out
}

// SafetyCheck runs Safety over every member of the session.
func (c *Core) SafetyCheck(s *Session) []MemberSafety {
	return c.Safety(s, s.Pkg.M.Members)
}

// safetyAcross runs Safety for the given members in every session.
func (c *Core) safetyAcross(sessions []*Session, members []manifest.Member) []MemberSafety {
	var out []MemberSafety
	for _, s := range sessions {
		out = append(out, c.Safety(s, members)...)
	}
	return out
}

// PackageSafety runs SafetyCheck over every session of a package, for the
// delete confirmation. Each row names its session.
func (c *Core) PackageSafety(p *Package) ([]MemberSafety, error) {
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return nil, err
	}
	return c.safetyAcross(sessions, p.M.Members), nil
}

// unsafeOf filters to members that would lose work.
func unsafeOf(all []MemberSafety) []MemberSafety {
	var out []MemberSafety
	for _, s := range all {
		if s.Unsafe() {
			out = append(out, s)
		}
	}
	return out
}

// DeleteOptions tunes Delete and DeleteSession.
type DeleteOptions struct {
	// Force deletes even when members have dirty files, untracked files or
	// unpushed commits.
	Force bool
	// DeleteBranches deletes session branches that are already merged into
	// their base. Unmerged branches are always kept, even with Force.
	DeleteBranches bool
}

// DeleteResult reports what happened to session branches.
type DeleteResult struct {
	BranchesDeleted []string `json:"branches_deleted"`
	BranchesKept    []string `json:"branches_kept"`
}

// sessionSteps returns the apply steps that tear one session down: remove
// each member's worktree, then the session directory. Safety checks are the
// caller's job. With prefix set, each step's member is labeled
// "session/member" so a package-wide run reads clearly.
func (c *Core) sessionSteps(s *Session, opts DeleteOptions, res *DeleteResult, prefix bool) ([]apply.Step, error) {
	var steps []apply.Step
	for _, mem := range s.Pkg.M.Members {
		ms, err := c.removeMemberSteps(s, mem, opts.Force, opts.DeleteBranches, res)
		if err != nil {
			return nil, err
		}
		steps = append(steps, ms...)
	}
	steps = append(steps, apply.Step{Name: "remove session", Run: func(func(string)) error {
		return os.RemoveAll(s.Dir)
	}})
	if prefix {
		labelSteps(steps, s.Name)
	}
	return steps, nil
}

// labelSteps prefixes each step's member with the session name, giving
// package-level steps the session name alone.
func labelSteps(steps []apply.Step, session string) {
	for i := range steps {
		if steps[i].Member == "" {
			steps[i].Member = session
		} else {
			steps[i].Member = session + "/" + steps[i].Member
		}
	}
}

// DeleteSession tears one session down: it checks every member for lost work
// (returning a *RefusedError unless forced), removes each worktree and
// prunes its source, optionally deletes merged branches, then removes the
// session directory. Source repos' working trees and checked-out branches
// are never touched, and the package and its other sessions are left alone.
func (c *Core) DeleteSession(s *Session, opts DeleteOptions) (DeleteResult, error) {
	var res DeleteResult
	if err := checkNotInside(s.Dir); err != nil {
		return res, err
	}
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}
	steps, err := c.sessionSteps(s, opts, &res, false)
	if err != nil {
		return res, err
	}
	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	c.Idx.RemoveSession(s.Pkg.Name, s.Name)
	_ = c.Idx.Save()
	return res, nil
}

// Delete tears a package down, every session first: it checks every member
// of every session for lost work (returning a *RefusedError unless forced),
// removes the worktrees, optionally deletes merged branches, then removes the
// package directory and index entry.
func (c *Core) Delete(p *Package, opts DeleteOptions) (DeleteResult, error) {
	var res DeleteResult
	if err := checkNotInside(p.Dir); err != nil {
		return res, err
	}
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return res, err
	}
	if bad := unsafeOf(c.safetyAcross(sessions, p.M.Members)); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}
	var steps []apply.Step
	for _, s := range sessions {
		ss, err := c.sessionSteps(s, opts, &res, true)
		if err != nil {
			return res, err
		}
		steps = append(steps, ss...)
	}
	steps = append(steps, apply.Step{Name: "remove package", Run: func(func(string)) error {
		return os.RemoveAll(p.Dir)
	}})
	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	c.Idx.RemovePackage(p.Name)
	_ = c.Idx.Save()
	return res, nil
}

// deleteMergedBranch deletes branch from src only when it is fully merged
// into base, or into the local landing branch into, which is where a finished
// session's work goes. It returns why a branch was kept, or "" if it did not
// exist.
func deleteMergedBranch(src, branch, base, into string) (keptReason string, deleted bool) {
	if !gitx.BranchExists(src, branch) {
		return "", false
	}
	ref, _ := gitx.ResolveDefaultRef(src, base)
	merged := gitx.RefExists(src, ref) && gitx.IsMerged(src, branch, ref)
	if !merged && into != "" && gitx.BranchExists(src, into) && gitx.IsMerged(src, branch, into) {
		merged = true
	}
	if !merged {
		return "not merged into " + ref, false
	}
	if err := gitx.DeleteBranch(src, branch, true); err != nil {
		return err.Error(), false
	}
	return "", true
}

// removeMemberSteps returns the apply steps that take one member out of a
// session: remove its worktree (forcibly only when force is set), prune the
// source, and optionally delete the session branch if it is merged into its
// base. Outcomes for branches are recorded into res. Safety checks are the
// caller's job.
func (c *Core) removeMemberSteps(s *Session, mem manifest.Member, force, deleteBranch bool, res *DeleteResult) ([]apply.Step, error) {
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return nil, err
	}
	dir := s.MemberDir(mem)
	steps := []apply.Step{{Member: mem.Name, Name: "worktree remove", Run: func(warn func(string)) error {
		if _, err := os.Stat(dir); err == nil {
			if err := gitx.WorktreeRemove(src, dir, force); err != nil {
				return err
			}
		}
		if _, err := os.Stat(src); err != nil {
			warn("source repo is missing; skipping prune")
			return nil
		}
		return gitx.WorktreePrune(src)
	}}}
	if deleteBranch && mem.Mode == manifest.ModeActive {
		steps = append(steps, apply.Step{Member: mem.Name, Name: "delete branch", Run: func(warn func(string)) error {
			into, _ := landingBranch(s, mem, src)
			kept, deleted := deleteMergedBranch(src, s.S.Branch, mem.Base, into)
			if deleted {
				res.BranchesDeleted = append(res.BranchesDeleted, mem.Name+":"+s.S.Branch)
			} else if kept != "" {
				res.BranchesKept = append(res.BranchesKept, mem.Name+":"+s.S.Branch)
				warn("kept branch " + s.S.Branch + ": " + kept)
			}
			return nil
		}})
	}
	return steps, nil
}
