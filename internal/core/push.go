package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// Statuses of one member in a PushResult.
const (
	// PushNothing: the member has nothing to publish. It has no commits of its
	// own, or the remote already has every one of them.
	PushNothing = "nothing"
	// PushReady: the member would push (plans), or was left unpushed because
	// the run was a dry run.
	PushReady = "ready"
	// PushPushed: the member's branch is now on its remote.
	PushPushed = "pushed"
	// PushMerged: the member's work has already been merged, so there is
	// nothing left to publish. The Reason says how.
	PushMerged = "merged"
	// PushBlocked: something must be fixed before the member can push. The
	// Reason says what.
	PushBlocked = "blocked"
	// PushFailed: git tried to push the member and refused or failed. The
	// Reason says why.
	PushFailed = "failed"
)

// MemberPush is what pushing a session does, or did, for one active member.
type MemberPush struct {
	Name string `json:"name"`
	// Branch is the branch the member is on in the session, which is the one
	// that gets pushed.
	Branch string `json:"branch"`
	// Remote is the remote the branch goes to, and Dest its name there, which
	// is Branch unless the branch tracks a differently named remote branch.
	Remote string `json:"remote,omitempty"`
	Dest   string `json:"dest,omitempty"`
	Status string `json:"status"`
	// Commits counts the commits on Branch that the remote does not have yet.
	Commits int `json:"commits"`
	// New is true when Dest does not exist on the remote as far as the last
	// fetch knows, so the push creates it.
	New bool `json:"new,omitempty"`
	// SetsUpstream is true when the push also makes Branch track Dest, which
	// it does for a branch that does not track one.
	SetsUpstream bool `json:"sets_upstream,omitempty"`
	// Reason explains a merged, blocked or failed member.
	Reason string `json:"reason,omitempty"`
	// Notes are warnings that do not stop the push, such as uncommitted work
	// that will not be part of it.
	Notes []string `json:"notes,omitempty"`
}

// PushOptions tunes Push.
type PushOptions struct {
	// Members names the active members to push. Empty means every active
	// member.
	Members []string
	// DryRun plans the push and changes nothing.
	DryRun bool
}

// PushResult reports a push or, for a dry run, what a push would do.
type PushResult struct {
	Package string       `json:"package"`
	Session string       `json:"session"`
	Members []MemberPush `json:"members"`
}

// PushBlockedError is returned when some members could not be pushed because
// something must be fixed first. The members that could be pushed were; Result
// says which is which. It matches ErrRefused, so the CLI exits with 2.
type PushBlockedError struct {
	Result PushResult
}

// Error implements error.
func (e *PushBlockedError) Error() string {
	var parts []string
	for _, m := range e.Result.Members {
		if m.Status == PushBlocked {
			parts = append(parts, fmt.Sprintf("%s (%s)", m.Name, m.Reason))
		}
	}
	return "refused: cannot push " + e.Result.Package + "/" + e.Result.Session + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *PushBlockedError) Is(target error) bool { return target == ErrRefused }

// pushing is the working state of one member while a push is planned and run:
// the public MemberPush plus the repo the git commands run in.
type pushing struct {
	MemberPush
	src string
}

// block marks the member as unable to push.
func (p *pushing) block(format string, args ...any) {
	p.Status = PushBlocked
	p.Reason = fmt.Sprintf(format, args...)
}

// note adds a warning that does not stop the push.
func (p *pushing) note(format string, args ...any) {
	p.Notes = append(p.Notes, fmt.Sprintf(format, args...))
}

// pushTarget is where a branch is pushed to and what it is compared against.
type pushTarget struct {
	remote, dest string
	// ref is the remote-tracking ref that mirrors dest, or "" when dest is not
	// known to exist on the remote.
	ref string
	// upstream is true when the branch already tracks a remote branch.
	upstream bool
}

// pushTargetFor works out where the local branch of the repo src is pushed. A
// branch with an upstream goes there, unless that is one of bases, the branches
// its work is meant to merge into. Any other goes to a branch of the same name
// on origin, or on the repo's only remote when it has no origin. The error is
// worded as the reason the member is blocked.
func pushTargetFor(refs gitx.Refs, src, branch string, bases []string) (pushTarget, error) {
	if p := publicationOf(refs, src, branch, bases); p.remote != "" {
		return pushTarget{remote: p.remote, dest: p.dest, ref: p.ref, upstream: p.upstream}, nil
	}
	remote := primaryRemote(src)
	if remote == "" {
		remotes := gitx.Remotes(src)
		if len(remotes) == 0 {
			return pushTarget{}, errors.New("its source repo has no remote to push to")
		}
		return pushTarget{}, fmt.Errorf("its source repo has several remotes (%s) and none is origin; give %s an upstream with `git branch --set-upstream-to`", strings.Join(remotes, ", "), branch)
	}
	return pushTarget{remote: remote, dest: branch}, nil
}

// pushCounts returns how many commits the local branch has that the target
// lacks, and how many the target has that the branch lacks. Both are measured
// against the last fetch. A target that does not exist yet is compared with
// everything the remote is known to have, so commits already on any of its
// branches are not counted as new.
func pushCounts(src, branch string, t pushTarget) (ahead, behind int, err error) {
	local := "refs/heads/" + branch
	if t.ref == "" {
		ahead, err = gitx.RevCount(src, local, "--not", "--remotes="+t.remote)
		return ahead, 0, err
	}
	return gitx.AheadBehind(src, local, t.ref)
}

// mergedHint is what to do about a member whose work has been merged.
const mergedHint = "remove the session with `vmono session rm`, and start a new one for more work"

// mergedReason says how a member's work was merged into base, and what to do.
func mergedReason(how, base string) string {
	verb := map[string]string{MergedSquash: "squash-merged", MergedRebase: "rebase-merged"}[how]
	return fmt.Sprintf("already %s into %s; %s", firstNonEmpty(verb, "merged"), shortRef(base), mergedHint)
}

// deletedReason says that the branch a member was pushed to is gone, which is
// usually what is left of a merged pull request.
func deletedReason(p publication, branch string) string {
	return fmt.Sprintf("%s/%s was deleted, probably because its pull request was merged; %s. To publish it again anyway, run `git branch --unset-upstream %s` in the member", p.remote, p.dest, mergedHint, branch)
}

// planPush inspects every active member the options select and decides,
// without changing anything, what pushing would do.
func (c *Core) planPush(s *Session, opts PushOptions) []*pushing {
	var out []*pushing
	for _, mem := range s.Pkg.M.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		if len(opts.Members) > 0 && !slices.Contains(opts.Members, mem.Name) {
			continue
		}
		p := &pushing{MemberPush: MemberPush{Name: mem.Name, Branch: s.BranchFor(mem.Name), Status: PushReady}}
		out = append(out, p)
		planPushMember(s, mem, p)
	}
	return out
}

// planPushMember fills p for one member. It stops at the first problem,
// leaving the member blocked, since later checks depend on the earlier ones.
func planPushMember(s *Session, mem manifest.Member, p *pushing) {
	src, err := paths.Abs(mem.Source)
	if err != nil {
		p.block("%v", err)
		return
	}
	p.src = src
	if !gitx.BranchExists(src, p.Branch) {
		p.block("its branch %s does not exist; run `vmono sync`", p.Branch)
		return
	}
	wt := s.MemberDir(mem)
	if st, err := gitx.StatusOf(wt); err == nil {
		switch {
		case gitx.RebaseInProgress(wt):
			p.block("a rebase is in progress; finish or abort it first")
			return
		case st.Branch != p.Branch:
			p.note("its worktree is on %s, and only %s is pushed", firstNonEmpty(st.Branch, "a detached HEAD"), p.Branch)
		case st.Dirty > 0 || st.Untracked > 0:
			p.note("%d modified and %d untracked files are not committed, so they are not pushed", st.Dirty, st.Untracked)
		}
	}
	refs := gitx.LiveRefs(src)
	base, err := baseOf(refs, s, mem, src, nil)
	if err != nil {
		p.block("%v", err)
		return
	}
	bases := baseBranches(s, mem, src)
	if slices.Contains(bases, p.Branch) {
		p.block("%s is the branch its work merges into, which is never pushed to", p.Branch)
		return
	}
	pub := publicationOf(refs, src, p.Branch, bases)
	if pub.published() || pub.deleted() {
		if how, into := mergedIntoAny(src, p.Branch, base.refs()); how != "" {
			p.Status, p.Reason = PushMerged, mergedReason(how, into)
			return
		}
	}
	if pub.deleted() {
		p.block("%s", deletedReason(pub, p.Branch))
		return
	}
	if !pub.published() {
		own, err := gitx.RevCount(src, append([]string{"refs/heads/" + p.Branch, "--not"}, base.refs()...)...)
		if err != nil {
			p.block("cannot count its commits: %v", err)
			return
		}
		if own == 0 {
			p.Status = PushNothing
			return
		}
	}
	t, err := pushTargetFor(refs, src, p.Branch, bases)
	if err != nil {
		p.block("%v", err)
		return
	}
	p.Remote, p.Dest, p.New, p.SetsUpstream = t.remote, t.dest, t.ref == "", !t.upstream
	ahead, behind, err := pushCounts(src, p.Branch, t)
	if err != nil {
		p.block("cannot compare %s with %s: %v", p.Branch, t.remote, err)
		return
	}
	p.Commits = ahead
	switch {
	case ahead > 0 && behind > 0:
		p.block("%s has diverged from %s/%s (%d ahead, %d behind as of the last fetch); run `vmono session update` first", p.Branch, t.remote, t.dest, ahead, behind)
	case !p.New && ahead == 0:
		p.Status = PushNothing
	}
}

// pushMembers returns the public view of the planned members.
func pushMembers(plan []*pushing) []MemberPush {
	out := make([]MemberPush, len(plan))
	for i, p := range plan {
		out[i] = p.MemberPush
	}
	return out
}

// checkPush runs the checks every push shares and returns the plan: git is
// recent enough and the options name active members.
func (c *Core) checkPush(s *Session, opts PushOptions) ([]*pushing, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if err := checkActive(s, opts.Members); err != nil {
		return nil, err
	}
	return c.planPush(s, opts), nil
}

// PlanPush reports what pushing the session would do, member by member,
// without changing anything. Members may come back blocked; that is the plan,
// not an error. It fails when the options name a member that is not an active
// one. The comparison with each remote is as of the last fetch.
func (c *Core) PlanPush(s *Session, opts PushOptions) (PushResult, error) {
	res := PushResult{Package: s.Pkg.Name, Session: s.Name}
	plan, err := c.checkPush(s, opts)
	if err != nil {
		return res, err
	}
	res.Members = pushMembers(plan)
	return res, nil
}

// Push publishes the branch each active member of the session is on to its
// remote: to the remote branch it tracks, or, for one that tracks none, to a
// branch of the same name on origin, which it then tracks. Members with nothing
// the remote lacks, or whose work has been merged, are skipped. It never
// forces, never pushes to the branch the work is meant to merge into, and never
// prompts for credentials. Work that is not committed stays where it is.
//
// Members are independent: each is pushed on its own, one that fails does not
// stop the others, and none is undone when another fails. The error joins what
// went wrong: one error per member git refused, wrapped in an *apply.StepError,
// and a *PushBlockedError for the members that could not be tried. Result says
// which is which in both cases. The post_push hook runs when any member was
// pushed; nothing is undone when it fails.
func (c *Core) Push(s *Session, opts PushOptions) (PushResult, error) {
	res := PushResult{Package: s.Pkg.Name, Session: s.Name}
	plan, err := c.checkPush(s, opts)
	if err != nil {
		return res, err
	}
	res.Members = pushMembers(plan)
	if opts.DryRun {
		return res, pushBlocked(plan, res)
	}

	var errs []error
	for _, p := range plan {
		if p.Status != PushReady {
			continue
		}
		step := apply.Step{Member: p.Name, Name: fmt.Sprintf("push %s → %s", p.Branch, p.Remote), Run: func(func(string)) error {
			return explainPush(p, gitx.Push(p.src, p.Remote, p.Branch, p.Dest, p.SetsUpstream))
		}}
		if err := (&apply.Runner{Events: c.Events}).Run([]apply.Step{step}); err != nil {
			p.Status, p.Reason = PushFailed, errors.Unwrap(err).Error()
			errs = append(errs, err)
			continue
		}
		p.Status = PushPushed
	}
	res.Members = pushMembers(plan)
	if err := pushBlocked(plan, res); err != nil {
		errs = append(errs, err)
	}
	var pushed []string
	for _, p := range plan {
		if p.Status == PushPushed {
			pushed = append(pushed, p.Name+":"+p.Dest)
		}
	}
	if len(pushed) > 0 {
		post := c.hookSteps(config.HookPostPush, s, map[string]string{"VMONO_PUSHED": strings.Join(pushed, " ")})
		if err := (&apply.Runner{Events: c.Events}).Run(post); err != nil {
			errs = append(errs, fmt.Errorf("pushed, but %w", err))
		}
	}
	return res, errors.Join(errs...)
}

// pushBlocked returns a *PushBlockedError when a planned member is blocked.
func pushBlocked(plan []*pushing, res PushResult) error {
	if slices.ContainsFunc(plan, func(p *pushing) bool { return p.Status == PushBlocked }) {
		return &PushBlockedError{Result: res}
	}
	return nil
}

// explainPush rewords a failed push into advice the user can act on: missing
// credentials, or a remote branch that has moved on. Any other error is
// returned as git gave it.
func explainPush(p *pushing, err error) error {
	switch {
	case err == nil:
		return nil
	case gitx.IsAuthError(err):
		return errors.New("authentication needed: pushes never prompt, so load your key into ssh-agent or sign in through your git credential helper, then retry")
	case gitx.IsNonFastForward(err):
		return fmt.Errorf("%s/%s has commits %s lacks; run `vmono session update`, then push again", p.Remote, p.Dest, p.Branch)
	}
	return err
}
