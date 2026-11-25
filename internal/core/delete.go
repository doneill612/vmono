package core

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// MemberSafety is the result of the pre-delete check for one member of one
// session: work that removing its worktree would discard, and what becomes of
// its branch.
type MemberSafety struct {
	// Session names the session the worktree belongs to.
	Session   string `json:"session,omitempty"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Missing   bool   `json:"missing,omitempty"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	// Unpushed counts commits only the worktree holds: made on a detached HEAD,
	// and on no branch, tag or remote. Commits on a branch are not among them,
	// since removing a worktree leaves the branch.
	Unpushed int    `json:"unpushed"`
	Error    string `json:"error,omitempty"`
	// Branch is the member's branch, "" when there is none, and Fate what
	// removing the member does with it: one of the Branch constants. Reason
	// says why.
	Branch string `json:"branch,omitempty"`
	Fate   string `json:"fate,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// What removing a member does with its branch.
const (
	// BranchEmpty: vmono made it, nothing was committed to it, and it is on no
	// remote, so it goes along.
	BranchEmpty = "empty"
	// BranchMerged: its work has been merged, so it goes when asked.
	BranchMerged = "merged"
	// BranchKept: it holds work that is not recognizably merged, so it stays.
	BranchKept = "kept"
)

// Label returns "session/member", or just the member when no session is set.
func (s MemberSafety) Label() string {
	if s.Session == "" {
		return s.Name
	}
	return s.Session + "/" + s.Name
}

// LossText words the work that would be lost.
func (s MemberSafety) LossText() string {
	text := fmt.Sprintf("%d dirty, %d untracked", s.Dirty, s.Untracked)
	if s.Unpushed > 0 {
		noun := "commits"
		if s.Unpushed == 1 {
			noun = "commit"
		}
		text += fmt.Sprintf(", %d %s on no branch", s.Unpushed, noun)
	}
	return text
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
			parts = append(parts, s.Label()+" ("+s.LossText()+")")
		}
	}
	return ErrRefused.Error() + ": " + strings.Join(parts, "; ") + " (use --force to override)"
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Safety checks the given members of a session for work that would be lost if
// their worktrees were removed: dirty files, untracked files, and commits that
// are on no branch, as those made on a detached HEAD are. Commits on a branch
// stay in the source repo when the worktree goes. It also says what becomes of
// each member's branch. Members whose directory is already gone are safe.
func (c *Core) Safety(s *Session, members []manifest.Member) []MemberSafety {
	out := make([]MemberSafety, 0, len(members))
	for _, mem := range members {
		ms := MemberSafety{Session: s.Name, Name: mem.Name, Mode: mem.Mode}
		if src, err := paths.Abs(mem.Source); err == nil {
			ms.Branch, ms.Fate, ms.Reason = branchFate(s, mem, src)
		}
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
		if st.Detached {
			if n, err := gitx.OnNoBranch(dir); err != nil {
				ms.Error = err.Error()
			} else {
				ms.Unpushed = n
			}
		}
		out = append(out, ms)
	}
	return out
}

// branchFate decides what removing an active member of a session does with its
// branch, and why. All are "" when there is no branch.
func branchFate(s *Session, mem manifest.Member, src string) (branch, fate, reason string) {
	branch = s.BranchFor(mem.Name)
	if mem.Mode != manifest.ModeActive || branch == "" || !gitx.BranchExists(src, branch) {
		return "", "", ""
	}
	refs := gitx.LiveRefs(src)
	bases := baseBranches(s, mem, src)
	base, err := baseOf(refs, s, mem, src, nil)
	if err != nil || slices.Contains(bases, branch) {
		return branch, BranchKept, "cannot tell whether it was merged"
	}
	local := "refs/heads/" + branch
	own, err := gitx.RevCount(src, append([]string{local, "--not"}, base.refs()...)...)
	if err != nil {
		return branch, BranchKept, "cannot tell whether it was merged"
	}
	pub := publicationOf(refs, src, branch, bases)
	if own == 0 && pub.remote == "" {
		if s.S.Members[mem.Name].Created {
			return branch, BranchEmpty, "nothing was committed to it"
		}
		return branch, BranchMerged, "nothing on it that is not on " + displayRef(src, base.ref)
	}
	if how, into := mergedIntoAny(src, local, base.refs()); how != "" {
		return branch, BranchMerged, "merged (" + how + ") into " + displayRef(src, into)
	}
	noun := "commits"
	if own == 1 {
		noun = "commit"
	}
	where := "on no remote"
	switch {
	case pub.deleted():
		where = pub.remote + "/" + pub.dest + " was deleted, so probably merged, but that cannot be confirmed"
	case pub.published():
		where = "on " + shortRef(pub.ref)
		if n, err := gitx.RevCount(src, local, "--not", pub.ref); err == nil && n > 0 {
			where = fmt.Sprintf("%d not on %s", n, shortRef(pub.ref))
		}
	}
	return branch, BranchKept, fmt.Sprintf("%d %s not merged, %s", own, noun, where)
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
// delete confirmation, and over an init workspace that was changed. Each row
// names its session.
func (c *Core) PackageSafety(p *Package) ([]MemberSafety, error) {
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return nil, err
	}
	_, changed, err := c.checkInit(p)
	if err != nil {
		return nil, err
	}
	return append(c.safetyAcross(sessions, p.M.Members), changed...), nil
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
	// commits on no branch.
	Force bool
	// DeleteBranches deletes the members' branches whose work has been merged.
	// Others are always kept, even with Force, but for the ones vmono made that
	// nothing was committed to, which always go.
	DeleteBranches bool
	// keepBranches keeps every branch, for a worktree that is about to be put
	// back.
	keepBranches bool
}

// BranchOutcome is what happened to one member's branch, and why.
type BranchOutcome struct {
	Session string `json:"session"`
	Member  string `json:"member"`
	Branch  string `json:"branch"`
	Reason  string `json:"reason"`
}

// DeleteResult reports what happened to the members' branches.
type DeleteResult struct {
	BranchesDeleted []BranchOutcome `json:"branches_deleted"`
	BranchesKept    []BranchOutcome `json:"branches_kept"`
}

// sessionSteps returns the apply steps that tear one session down: remove
// each member's worktree, then the session directory. Safety checks are the
// caller's job. With prefix set, each step's member is labeled
// "session/member" so a package-wide run reads clearly.
func (c *Core) sessionSteps(s *Session, opts DeleteOptions, res *DeleteResult, prefix bool) ([]apply.Step, error) {
	var steps []apply.Step
	for _, mem := range s.Pkg.M.Members {
		ms, err := c.removeMemberSteps(s, mem, opts, res)
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

// terminals finds the terminals the sessions' harnesses run in, and returns
// the function that closes them. It must be called before the sessions are
// removed, and the function after everything else has been done and saved:
// vmono may be running in one of them, for instance in a popup over it, and
// closing it then ends vmono.
func (c *Core) terminals(sessions ...*Session) func() {
	var steps []apply.Step
	for _, s := range sessions {
		if c.FindTerminal == nil {
			break
		}
		closeIt := c.FindTerminal(s.Pkg.Name, s.Name, s.Dir)
		if closeIt == nil {
			continue
		}
		step := apply.Step{Name: "close terminal", RunOut: func(_, out func(string)) error {
			for _, note := range closeIt() {
				out(note)
			}
			return nil
		}}
		if len(sessions) > 1 {
			step.Member = s.Name
		}
		steps = append(steps, step)
	}
	return func() { _ = (&apply.Runner{Events: c.Events}).Run(steps) }
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
// (returning a *RefusedError unless forced), runs the pre_remove hook, which may
// veto it, removes each worktree and prunes its source, deletes branches with
// nothing on them and optionally merged ones, then removes the session
// directory. Source repos' working trees and checked-out branches
// are never touched, and the package and its other sessions are left alone.
func (c *Core) DeleteSession(s *Session, opts DeleteOptions) (DeleteResult, error) {
	var res DeleteResult
	if err := checkNotInside(s.Dir); err != nil {
		return res, err
	}
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}
	if err := c.preRemove(s); err != nil {
		return res, err
	}
	steps, err := c.sessionSteps(s, opts, &res, false)
	if err != nil {
		return res, err
	}
	closeTerminals := c.terminals(s)
	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	c.Idx.RemoveSession(s.Pkg.Name, s.Name)
	_ = c.Idx.Save()
	closeTerminals()
	return res, nil
}

// Delete tears a package down, every session first: it checks every member
// of every session for lost work (returning a *RefusedError unless forced),
// removes the worktrees, those of a leftover init workspace included,
// deletes branches with nothing on them and optionally merged ones, then removes
// the package directory and index entry.
func (c *Core) Delete(p *Package, opts DeleteOptions) (DeleteResult, error) {
	var res DeleteResult
	if err := checkNotInside(p.Dir); err != nil {
		return res, err
	}
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return res, err
	}
	initWS, changed, err := c.checkInit(p)
	if err != nil {
		return res, err
	}
	if bad := append(unsafeOf(c.safetyAcross(sessions, p.M.Members)), changed...); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}
	if err := c.preRemove(sessions...); err != nil {
		return res, err
	}
	var steps []apply.Step
	for _, s := range sessions {
		ss, err := c.sessionSteps(s, opts, &res, true)
		if err != nil {
			return res, err
		}
		steps = append(steps, ss...)
	}
	if initWS != nil {
		ss, err := c.initRemovalSteps(initWS, opts.Force)
		if err != nil {
			return res, err
		}
		steps = append(steps, ss...)
	}
	steps = append(steps, apply.Step{Name: "remove package", Run: func(func(string)) error {
		return os.RemoveAll(p.Dir)
	}})
	// An initialization runs in the package directory, and leaves its shell
	// there under herdr.
	closeTerminals := c.terminals(append(sessions, &Session{Pkg: p, Name: InitName, Dir: p.Dir})...)
	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	c.Idx.RemovePackage(p.Name)
	_ = c.Idx.Save()
	closeTerminals()
	return res, nil
}

// removeMemberSteps returns the apply steps that take one member out of a
// session: run its teardown commands, remove its worktree (forcibly only when
// forced), prune the source, and delete its branch when branchFate and opts
// allow it. Outcomes for branches are recorded into res. Safety checks are the
// caller's job.
func (c *Core) removeMemberSteps(s *Session, mem manifest.Member, opts DeleteOptions, res *DeleteResult) ([]apply.Step, error) {
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return nil, err
	}
	dir := s.MemberDir(mem)
	steps := append(c.memberHookSteps(s, mem, src, "teardown", mem.Teardown), apply.Step{Member: mem.Name, Name: "worktree remove", Run: func(warn func(string)) error {
		if _, err := os.Stat(dir); err == nil {
			if err := gitx.WorktreeRemove(src, dir, opts.Force); err != nil {
				return err
			}
		}
		if _, err := os.Stat(src); err != nil {
			warn("source repo is missing; skipping prune")
			return nil
		}
		return gitx.WorktreePrune(src)
	}})
	if opts.keepBranches || mem.Mode != manifest.ModeActive {
		return steps, nil
	}
	return append(steps, apply.Step{Member: mem.Name, Name: "branch", RunOut: func(warn, out func(string)) error {
		branch, fate, reason := branchFate(s, mem, src)
		if branch == "" {
			return nil
		}
		o := BranchOutcome{Session: s.Name, Member: mem.Name, Branch: branch, Reason: reason}
		if fate == BranchEmpty || fate == BranchMerged && opts.DeleteBranches {
			err := gitx.DeleteBranch(src, branch, true)
			if err == nil {
				res.BranchesDeleted = append(res.BranchesDeleted, o)
				out("deleted " + branch + ": " + reason)
				return nil
			}
			o.Reason = oneLine(err.Error())
			warn("kept " + branch + ": " + o.Reason)
		} else {
			out("kept " + branch + ": " + reason)
		}
		res.BranchesKept = append(res.BranchesKept, o)
		return nil
	}}), nil
}
