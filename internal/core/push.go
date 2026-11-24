package core

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// Statuses of one member in a PushResult.
const (
	// PushNothing: the member has nothing to publish. Its landing branch does
	// not exist locally, or the remote already has every commit on it.
	PushNothing = "nothing"
	// PushReady: the member would push (plans), or was left unpushed because
	// the run was a dry run.
	PushReady = "ready"
	// PushPushed: the member's landing branch is now on its remote.
	PushPushed = "pushed"
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
	// Branch is the member's landing branch in its source repo, the branch
	// that gets pushed. The session's own branch is never pushed.
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
	// it does for a branch with no upstream.
	SetsUpstream bool `json:"sets_upstream,omitempty"`
	// Reason explains a blocked or failed member.
	Reason string `json:"reason,omitempty"`
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

// PushNotFinishedError is returned when a push is asked of a session that is
// not finished: it was never finished, or has uncommitted work or commits
// that have not landed since. Nothing has been pushed. It matches ErrRefused,
// so the CLI exits with 2.
type PushNotFinishedError struct {
	Package, Session string
}

// Error implements error.
func (e *PushNotFinishedError) Error() string {
	label := e.Package + "/" + e.Session
	return "refused: session " + label + " is not finished, so there is nothing landed to push; finish it first with `vmono session finish " + label + "`"
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *PushNotFinishedError) Is(target error) bool { return target == ErrRefused }

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

// pushTarget is where a landing branch is pushed to and what it is compared
// against.
type pushTarget struct {
	remote, dest string
	// ref is the remote-tracking ref that mirrors dest, or "" when dest is not
	// known to exist on the remote.
	ref string
	// upstream is true when the branch already tracks a remote branch.
	upstream bool
}

// pushTargetFor works out where the local branch into of the repo src is
// pushed. A branch with an upstream goes there. One without goes to a branch
// of the same name on origin, or on the repo's only remote when it has no
// origin. The error is worded as the reason the member is blocked.
func pushTargetFor(src, into string) (pushTarget, error) {
	if tr, ok := gitx.UpstreamOf(src, into); ok {
		t := pushTarget{remote: tr.Remote, dest: tr.Branch, upstream: true}
		if gitx.RefExists(src, tr.Ref) {
			t.ref = tr.Ref
		}
		return t, nil
	}
	remote := primaryRemote(src)
	if remote == "" {
		remotes := gitx.Remotes(src)
		if len(remotes) == 0 {
			return pushTarget{}, errors.New("its source repo has no remote to push to")
		}
		return pushTarget{}, fmt.Errorf("its source repo has several remotes (%s) and none is origin; give %s an upstream with `git branch --set-upstream-to`", strings.Join(remotes, ", "), into)
	}
	t := pushTarget{remote: remote, dest: into}
	if ref := "refs/remotes/" + remote + "/" + into; gitx.RefExists(src, ref) {
		t.ref = ref
	}
	return t, nil
}

// pushCounts returns how many commits the local branch into has that the
// target lacks, and how many the target has that the branch lacks. Both are
// measured against the last fetch. A target that does not exist yet is
// compared with everything the remote is known to have, so commits already on
// any of its branches are not counted as new.
func pushCounts(src, into string, t pushTarget) (ahead, behind int, err error) {
	local := "refs/heads/" + into
	if t.ref == "" {
		ahead, err = gitx.RevCount(src, local, "--not", "--remotes="+t.remote)
		return ahead, 0, err
	}
	if ahead, err = gitx.RevCount(src, local, "--not", t.ref); err != nil {
		return 0, 0, err
	}
	behind, err = gitx.RevCount(src, t.ref, "--not", local)
	return ahead, behind, err
}

// unpushedOn returns where the local branch into of the repo src would be
// pushed, as "remote/branch", and how many of its commits that branch lacks as
// of the last fetch. It returns "" and 0 for a branch that does not exist or
// has nowhere to go, which status reports as nothing to push rather than as
// an error.
func unpushedOn(src, into string) (target string, n int) {
	if !gitx.BranchExists(src, into) {
		return "", 0
	}
	t, err := pushTargetFor(src, into)
	if err != nil {
		return "", 0
	}
	ahead, _, err := pushCounts(src, into, t)
	if err != nil {
		return "", 0
	}
	return t.remote + "/" + t.dest, ahead
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
		p := &pushing{MemberPush: MemberPush{Name: mem.Name, Status: PushReady}}
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
	if p.Branch, _ = landingBranch(s, mem, src); p.Branch == "" {
		p.block("cannot tell which branch it lands on")
		return
	}
	if !gitx.BranchExists(src, p.Branch) {
		p.Status = PushNothing
		return
	}
	t, err := pushTargetFor(src, p.Branch)
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
		p.block("%s has diverged from %s/%s (%d ahead, %d behind as of the last fetch); pull or rebase it in %s first", p.Branch, t.remote, t.dest, ahead, behind, src)
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
// recent enough, the options name active members, and the session is
// finished.
func (c *Core) checkPush(s *Session, opts PushOptions) ([]*pushing, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if err := checkActive(s, opts.Members); err != nil {
		return nil, err
	}
	if c.SessionState(s) != SessionFinished {
		return nil, &PushNotFinishedError{Package: s.Pkg.Name, Session: s.Name}
	}
	return c.planPush(s, opts), nil
}

// PlanPush reports what pushing the session would do, member by member,
// without changing anything. Members may come back blocked; that is the plan,
// not an error. It fails when the session is not finished or the options name
// a member that is not an active one. The comparison with each remote is as of
// the last fetch.
func (c *Core) PlanPush(s *Session, opts PushOptions) (PushResult, error) {
	res := PushResult{Package: s.Pkg.Name, Session: s.Name}
	plan, err := c.checkPush(s, opts)
	if err != nil {
		return res, err
	}
	res.Members = pushMembers(plan)
	return res, nil
}

// Push publishes the landing branch of each active member of a finished
// session to its remote, from the member's source repo: to the remote branch
// it tracks, or, for a branch with no upstream, to a branch of the same name
// on origin, which it then tracks. Members with nothing the remote lacks are
// skipped. It never forces, never pushes the session's own branch, and never
// prompts for credentials.
//
// Members are independent, unlike in Finish: each is pushed on its own, one
// that fails does not stop the others, and none is undone when another fails.
// The error joins what went wrong: one error per member git refused, wrapped in
// an *apply.StepError, and a *PushBlockedError for the members that could not
// be tried. Result says which is which in both cases. It refuses a session
// that is not finished with *PushNotFinishedError.
func (c *Core) Push(s *Session, opts PushOptions) (PushResult, error) {
	res := PushResult{Package: s.Pkg.Name, Session: s.Name}
	plan, err := c.checkPush(s, opts)
	if err != nil {
		return res, err
	}
	res.Members = pushMembers(plan)
	if opts.DryRun {
		return res, nil
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
	for _, p := range plan {
		if p.Status == PushBlocked {
			errs = append(errs, &PushBlockedError{Result: res})
			break
		}
	}
	return res, errors.Join(errs...)
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
		return fmt.Errorf("%s/%s has commits %s lacks; pull or rebase in %s, then push again", p.Remote, p.Dest, p.Branch, p.src)
	}
	return err
}
