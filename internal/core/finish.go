package core

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// Statuses of one member in a FinishResult.
const (
	// LandNothing: the member has no commits of its own to land.
	LandNothing = "nothing"
	// LandReady: the member would land (plans), or was rebased but has not
	// moved its target because another member stopped the finish.
	LandReady = "ready"
	// LandLanded: the member's commits are on its landing branch.
	LandLanded = "landed"
	// LandBlocked: something must be fixed before the member can land. The
	// Reason says what.
	LandBlocked = "blocked"
	// LandConflict: rebasing the member stopped on conflicts, which are left
	// in its worktree for the user or the agent to resolve.
	LandConflict = "conflict"
)

// Ways a landing branch moves.
const (
	// ViaRefUpdate: the branch is checked out nowhere, so only its ref moves.
	ViaRefUpdate = "ref update"
	// ViaFastForward: the branch is checked out in a clean worktree, which
	// is fast-forwarded along with it.
	ViaFastForward = "fast-forward"
)

// MemberLanding is what finishing a session does, or did, for one active
// member.
type MemberLanding struct {
	Name string `json:"name"`
	// Into is the local branch in the member's source repo the work lands on.
	Into   string `json:"into"`
	Status string `json:"status"`
	// Commits counts the member's own commits: reachable from the session
	// branch but from neither the member's base nor its landing branch.
	Commits int `json:"commits"`
	// Create is true when Into does not exist locally and finish creates it.
	Create bool `json:"create,omitempty"`
	// Via says how the landing branch moves; empty when nothing lands.
	Via string `json:"via,omitempty"`
	// CheckedOutAt is the worktree that has Into checked out, if any.
	CheckedOutAt string `json:"checked_out_at,omitempty"`
	// Reason explains a blocked or conflicted member.
	Reason string `json:"reason,omitempty"`
	// Conflicts lists the files a stopped rebase left unresolved.
	Conflicts []string `json:"conflicts,omitempty"`
	// Notes are warnings that do not stop the finish.
	Notes []string `json:"notes,omitempty"`
}

// FinishOptions tunes Finish.
type FinishOptions struct {
	// Into overrides the landing branch for every member and is recorded in
	// the session file, so later safety checks look at the same branch. Empty
	// keeps the session's own, or each member's base branch.
	Into string
	// Close removes the session after its work has landed, deleting the
	// session branches, which then count as merged.
	Close bool
	// DryRun plans the finish and changes nothing.
	DryRun bool
}

// FinishResult reports a finish or, for a dry run, what a finish would do.
type FinishResult struct {
	Package string          `json:"package"`
	Session string          `json:"session"`
	Members []MemberLanding `json:"members"`
	// Finished is true once the work has landed and the session is marked.
	Finished bool `json:"finished"`
	// Closed is true when the session was removed as well.
	Closed bool          `json:"closed,omitempty"`
	Delete *DeleteResult `json:"delete,omitempty"`
}

// FinishBlockedError is returned when a finish cannot land, either because a
// member needs attention before anything moves, or because a rebase stopped
// on conflicts. Nothing has landed when it is returned. It matches
// ErrRefused, so the CLI exits with 2; Result says which members to fix.
type FinishBlockedError struct {
	Result FinishResult
}

// Error implements error.
func (e *FinishBlockedError) Error() string {
	var parts []string
	for _, m := range e.Result.Members {
		switch m.Status {
		case LandBlocked:
			parts = append(parts, fmt.Sprintf("%s (%s)", m.Name, m.Reason))
		case LandConflict:
			parts = append(parts, fmt.Sprintf("%s (rebase onto %s stopped on conflicts in %s)", m.Name, m.Into, strings.Join(m.Conflicts, ", ")))
		}
	}
	return ErrRefused.Error() + ": cannot finish " + e.Result.Package + "/" + e.Result.Session + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *FinishBlockedError) Is(target error) bool { return target == ErrRefused }

// landing is the working state of one member while a finish is planned and
// run: the public MemberLanding plus the refs and paths the steps need.
type landing struct {
	MemberLanding
	src, wt string
	// target is the ref the commits are rebased onto: the landing branch, its
	// copy on origin, or, for a branch that is new, the member's base.
	target string
	// fork is where the commits to replay start; empty when the session
	// already contains target and the branch only has to fast-forward.
	fork string
}

// block marks the member as unable to land.
func (l *landing) block(format string, args ...any) {
	l.Status = LandBlocked
	l.Reason = fmt.Sprintf(format, args...)
}

// planFinish inspects every active member of the session and decides, without
// changing anything, what finishing would do. into overrides the landing
// branch when non-empty.
func (c *Core) planFinish(s *Session, into string) []*landing {
	var out []*landing
	for _, mem := range s.Pkg.M.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		l := &landing{MemberLanding: MemberLanding{Name: mem.Name, Status: LandReady}}
		out = append(out, l)
		c.planMember(s, mem, into, l)
	}
	return out
}

// planMember fills l for one member. It stops at the first problem, leaving
// the member blocked, since later checks depend on the earlier ones.
func (c *Core) planMember(s *Session, mem manifest.Member, into string, l *landing) {
	src, err := paths.Abs(mem.Source)
	if err != nil {
		l.block("%v", err)
		return
	}
	l.src, l.wt = src, s.MemberDir(mem)
	if _, err := os.Stat(l.wt); err != nil {
		l.block("its worktree is missing; run `vmono sync`")
		return
	}
	l.Into = into
	if l.Into == "" {
		l.Into, _ = landingBranch(s, mem, src)
	}
	switch {
	case l.Into == "":
		l.block("cannot tell which branch to land on; pass --into")
		return
	case !gitx.ValidBranchName(src, l.Into):
		l.block("%q is not a valid branch name", l.Into)
		return
	case l.Into == s.S.Branch:
		l.block("%q is the session's own branch", l.Into)
		return
	}

	if gitx.RebaseInProgress(l.wt) {
		if files, _ := gitx.ConflictedFiles(l.wt); len(files) > 0 {
			l.Status, l.Conflicts = LandConflict, files
			l.Reason = "a rebase is stopped on conflicts; resolve them, `git add`, `git rebase --continue`, then finish again"
			return
		}
		l.block("a rebase is in progress; run `git rebase --continue` in %s, then finish again", l.wt)
		return
	}
	st, err := gitx.StatusOf(l.wt)
	switch {
	case err != nil:
		l.block("cannot read its state: %v", err)
		return
	case st.Detached || st.Branch != s.S.Branch:
		l.block("its worktree is on %s, not the session branch %s", firstNonEmpty(st.Branch, "a detached HEAD"), s.S.Branch)
		return
	case st.Dirty > 0 || st.Untracked > 0:
		l.block("uncommitted changes (%d modified, %d untracked); commit them first", st.Dirty, st.Untracked)
		return
	}

	base, err := resolveRef(src, mem.Base, nil)
	if err != nil {
		l.block("%v", err)
		return
	}
	exists := gitx.BranchExists(src, l.Into)
	switch {
	case exists:
		l.target = "refs/heads/" + l.Into
	case gitx.RefExists(src, "refs/remotes/origin/"+l.Into):
		l.target, l.Create = "refs/remotes/origin/"+l.Into, true
	default:
		l.target, l.Create = base, true
	}
	if !exists {
		if other, clash := gitx.BranchRefConflict(src, l.Into); clash {
			l.block("branch %q conflicts with existing branch %q (git cannot hold both)", l.Into, other)
			return
		}
	}

	if l.Commits, err = gitx.RevCount(l.wt, "HEAD", "--not", base, l.target); err != nil {
		l.block("cannot count its commits: %v", err)
		return
	}
	if l.Commits == 0 {
		l.Status = LandNothing
		return
	}

	if !gitx.IsMerged(l.wt, l.target, "HEAD") {
		if l.fork, err = forkPoint(l.wt, base, l.target); err != nil {
			l.block("%v", err)
			return
		}
	}

	l.Via = ViaRefUpdate
	if exists {
		at, err := gitx.BranchCheckedOutAt(src, l.Into)
		if err != nil {
			l.block("%v", err)
			return
		}
		if at != "" {
			l.CheckedOutAt, l.Via = at, ViaFastForward
			if ts, err := gitx.StatusOf(at); err != nil {
				l.block("cannot read the worktree where %s is checked out: %v", l.Into, err)
				return
			} else if ts.Dirty > 0 {
				l.block("%s is checked out at %s with uncommitted changes; commit or stash them first", l.Into, at)
				return
			}
		}
		if behind, err := gitx.RevCount(src, l.Into+"@{upstream}", "--not", l.target); err == nil && behind > 0 {
			l.Notes = append(l.Notes, fmt.Sprintf("%s is %d commit(s) behind its upstream; `git -C %s pull --ff-only` first if you want them", l.Into, behind, src))
		}
	}
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// forkPoint returns the commit after which a session's own commits begin: the
// later of its merge-base with the member's base and with the landing target.
// Replaying from the base's merge-base keeps commits that already sit on the
// base, which the session was built on, from being replayed as new ones.
func forkPoint(wt, base, target string) (string, error) {
	onTarget, err := gitx.MergeBase(wt, "HEAD", target)
	if err != nil {
		return "", fmt.Errorf("the session has no common history with %s", target)
	}
	onBase, err := gitx.MergeBase(wt, "HEAD", base)
	if err == nil && gitx.IsMerged(wt, onTarget, onBase) {
		return onBase, nil
	}
	return onTarget, nil
}

// membersOf returns the public view of the planned members.
func membersOf(plan []*landing) []MemberLanding {
	out := make([]MemberLanding, len(plan))
	for i, l := range plan {
		out[i] = l.MemberLanding
	}
	return out
}

// blocked reports whether any planned member stops the finish.
func blocked(plan []*landing) bool {
	for _, l := range plan {
		if l.Status == LandBlocked || l.Status == LandConflict {
			return true
		}
	}
	return false
}

// PlanFinish reports what finishing the session would do, member by member,
// without changing anything. Members may come back blocked; that is the plan,
// not an error. into overrides the landing branch like FinishOptions.Into.
func (c *Core) PlanFinish(s *Session, into string) FinishResult {
	return FinishResult{Package: s.Pkg.Name, Session: s.Name, Members: membersOf(c.planFinish(s, into))}
}

// Finish lands the session's work: for every active member with commits of
// its own, those commits are rebased onto the member's landing branch inside
// the session's worktree, and the landing branch is then moved to the result
// in the member's source repo. A branch checked out in a clean worktree is
// fast-forwarded with it; any other has its ref updated in place; one that
// does not exist yet is created. The session is then marked finished.
//
// It is all or nothing for the landing branches. Every member is checked
// before anything moves, and a rebase that stops on conflicts leaves them in
// the worktree and returns *FinishBlockedError without moving any branch.
// Running Finish again after resolving them continues where it stopped, since
// members already rebased need no further rebase. If moving the branches
// fails partway, the ones already moved are put back.
//
// The session, its worktrees and its branch stay in place unless
// opts.Close is set.
func (c *Core) Finish(s *Session, opts FinishOptions) (FinishResult, error) {
	res := FinishResult{Package: s.Pkg.Name, Session: s.Name}
	if err := gitx.CheckVersion(); err != nil {
		return res, err
	}
	if opts.Into != "" {
		probe := *s.S
		probe.Into = opts.Into
		if err := probe.Validate(); err != nil {
			return res, err
		}
	}

	plan := c.planFinish(s, opts.Into)
	res.Members = membersOf(plan)
	if blocked(plan) {
		return res, &FinishBlockedError{Result: res}
	}
	var touched []*landing
	for _, l := range plan {
		if l.Status == LandReady {
			touched = append(touched, l)
		}
	}
	if opts.DryRun {
		return res, nil
	}

	if err := c.rebaseMembers(touched); err != nil {
		res.Members = membersOf(plan)
		if blocked(plan) {
			return res, &FinishBlockedError{Result: res}
		}
		return res, err
	}
	tips, err := checkRebased(touched)
	if err != nil {
		res.Members = membersOf(plan)
		return res, &FinishBlockedError{Result: res}
	}
	if err := c.advanceTargets(s, touched, tips); err != nil {
		return res, err
	}
	for _, l := range touched {
		l.Status = LandLanded
	}
	res.Members = membersOf(plan)

	if err := c.markFinished(s, opts.Into); err != nil {
		return res, err
	}
	res.Finished = true

	if opts.Close {
		del, err := c.DeleteSession(s, DeleteOptions{DeleteBranches: true})
		res.Delete = &del
		if err != nil {
			return res, fmt.Errorf("landed, but the session could not be closed: %w", err)
		}
		res.Closed = true
	}
	return res, nil
}

// rebaseMembers rebases each member that needs it, in order, stopping at the
// first failure. A stop on conflicts marks that member LandConflict and
// leaves the rebase in progress in its worktree. Members rebased before it
// stay rebased: running the finish again finds them up to date.
func (c *Core) rebaseMembers(touched []*landing) error {
	var steps []apply.Step
	for _, l := range touched {
		if l.fork == "" {
			continue
		}
		steps = append(steps, apply.Step{Member: l.Name, Name: "rebase onto " + l.Into, Run: func(func(string)) error {
			err := gitx.RebaseOnto(l.wt, l.target, l.fork)
			if err == nil {
				return nil
			}
			if gitx.RebaseInProgress(l.wt) {
				l.Status = LandConflict
				l.Conflicts, _ = gitx.ConflictedFiles(l.wt)
				l.Reason = "rebase stopped on conflicts; resolve them, `git add`, `git rebase --continue`, then finish again"
			} else {
				l.block("rebase failed: %v", err)
			}
			return err
		}})
	}
	return (&apply.Runner{Events: c.Events}).Run(steps)
}

// checkRebased returns each member's new tip, and confirms the landing target
// is an ancestor of it, which is what makes moving the branch a fast-forward.
func checkRebased(touched []*landing) (map[string]string, error) {
	tips := map[string]string{}
	for _, l := range touched {
		tip, err := gitx.ResolveRef(l.wt, "HEAD")
		if err != nil {
			l.block("cannot read its branch: %v", err)
			return nil, err
		}
		if !gitx.IsMerged(l.wt, l.target, tip) {
			l.block("%s is not an ancestor of the rebased branch", l.Into)
			return nil, errors.New("not a fast-forward")
		}
		tips[l.Name] = tip
	}
	return tips, nil
}

// advanceTargets moves every member's landing branch to its tip as one apply
// run with rollback. Branches checked out nowhere move first, by
// compare-and-swap on the ref; branches checked out in a worktree are
// fast-forwarded last, since undoing those is the most disruptive.
func (c *Core) advanceTargets(s *Session, touched []*landing, tips map[string]string) error {
	ordered := append([]*landing{}, touched...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CheckedOutAt == "" && ordered[j].CheckedOutAt != ""
	})
	reason := "vmono: land session " + s.Label()
	var steps []apply.Step
	for _, l := range ordered {
		tip := tips[l.Name]
		ref := "refs/heads/" + l.Into
		old := ""
		if !l.Create {
			old, _ = gitx.ResolveRef(l.src, ref)
		}
		if l.CheckedOutAt != "" {
			steps = append(steps, apply.Step{Member: l.Name, Name: "fast-forward " + l.Into, Run: func(func(string)) error {
				return gitx.MergeFFOnly(l.CheckedOutAt, tip)
			}, Undo: func() error {
				return gitx.ResetKeep(l.CheckedOutAt, old)
			}})
			continue
		}
		steps = append(steps, apply.Step{Member: l.Name, Name: "update " + l.Into, Run: func(func(string)) error {
			return gitx.UpdateRefCAS(l.src, ref, tip, old, reason)
		}, Undo: func() error {
			if old == "" {
				return gitx.DeleteBranch(l.src, l.Into, true)
			}
			return gitx.UpdateRefCAS(l.src, ref, old, tip, "vmono: undo landing of session "+s.Label())
		}})
	}
	return (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps)
}

// markFinished records the finish time, and a landing branch given at finish
// time, in the session file, and commits the file to the session repo when
// auto_commit is on.
func (c *Core) markFinished(s *Session, into string) error {
	s.S.Finished = time.Now().Truncate(time.Second)
	if into != "" {
		s.S.Into = into
	}
	if err := manifest.SaveSession(s.Dir, s.S); err != nil {
		return fmt.Errorf("landed, but the session file could not be updated: %w", err)
	}
	if c.Cfg.AutoCommit {
		if _, err := gitx.CommitAll(s.Dir, fmt.Sprintf("vmono: finish session %s (%s)", s.Name, c.Author), ""); err != nil {
			return fmt.Errorf("landed, but the session repo could not be committed: %w", err)
		}
	}
	return nil
}
