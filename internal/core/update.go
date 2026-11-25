package core

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// Statuses of one member in an UpdateResult.
const (
	// UpdateCurrent: the member already has everything on its target.
	UpdateCurrent = "up-to-date"
	// UpdateReady: the member would be updated (dry runs).
	UpdateReady = "ready"
	// UpdateDone: the member's commits now sit on top of its target.
	UpdateDone = "updated"
	// UpdateMerged: the member's work has been merged, so there is nothing left
	// to update. The Reason says how.
	UpdateMerged = "merged"
	// UpdateSkipped: the member was left alone. The Reason says why.
	UpdateSkipped = "skipped"
	// UpdateConflict: rebasing the member conflicted. The rebase was aborted
	// and the member is as it was.
	UpdateConflict = "conflict"
	// UpdateFailed: git failed for some other reason. The Reason says why.
	UpdateFailed = "failed"
)

// MemberUpdate is what updating a session does, or did, for one active member.
type MemberUpdate struct {
	Name string `json:"name"`
	// Onto is what the member is updated to, such as "origin/main".
	Onto   string `json:"onto,omitempty"`
	Status string `json:"status"`
	// Behind counts the commits on Onto the session lacked.
	Behind int `json:"behind"`
	// Commits counts the branch's own commits, which a rebase replays on top of
	// Onto.
	Commits int `json:"commits"`
	// Merge is true when the member is updated by merging instead of rebasing,
	// because its branch has been published.
	Merge bool `json:"merge,omitempty"`
	// Reason explains a merged, skipped, conflicted or failed member.
	Reason string `json:"reason,omitempty"`
	// Conflicts lists the files an aborted rebase or merge conflicted in.
	Conflicts []string `json:"conflicts,omitempty"`
	// Notes say what else changed, such as what the branch is built on.
	Notes []string `json:"notes,omitempty"`
}

// UpdateOptions tunes Update.
type UpdateOptions struct {
	// Members names the active members to update. Empty means every one.
	Members []string
	// NoFetch updates to the refs already on disk.
	NoFetch bool
	// DryRun reports what would be updated and moves no branch. It still
	// fetches, unless NoFetch is set, since the plan is stale otherwise.
	DryRun bool
}

// UpdateResult reports an update or, for a dry run, what one would do.
type UpdateResult struct {
	Package string         `json:"package"`
	Session string         `json:"session"`
	Members []MemberUpdate `json:"members"`
}

// UpdateIncompleteError is returned when some members could not be updated
// because something must be fixed first. The others were updated; Result says
// which is which. It matches ErrRefused, so the CLI exits with 2.
type UpdateIncompleteError struct {
	Result UpdateResult
}

// Error implements error.
func (e *UpdateIncompleteError) Error() string {
	var parts []string
	for _, m := range e.Result.Members {
		if m.Status == UpdateSkipped || m.Status == UpdateConflict {
			parts = append(parts, fmt.Sprintf("%s (%s)", m.Name, m.Reason))
		}
	}
	return "refused: cannot update all of " + e.Result.Package + "/" + e.Result.Session + ": " + strings.Join(parts, "; ")
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *UpdateIncompleteError) Is(target error) bool { return target == ErrRefused }

// updating is the working state of one member while an update is planned and
// run: the public MemberUpdate plus the refs and paths the update needs.
type updating struct {
	MemberUpdate
	src, wt, branch string
	// target and fork are what a rebase replays onto and after.
	target, fork string
	// merges are the refs a published member merges, in order.
	merges []string
	// retarget is true when the branch moves from what the session built it on
	// to the member's own base, and tip is what it is built on afterwards.
	retarget bool
	tip      string
	// track is the remote branch to start tracking, for a published branch that
	// tracks none.
	track string
}

// skip marks the member as left alone.
func (u *updating) skip(format string, args ...any) {
	u.Status, u.Reason = UpdateSkipped, fmt.Sprintf(format, args...)
}

// Update brings each active member of the session up to date. The sources are
// fetched, and a branch that is not published is then rebased, inside the
// session's worktree, onto what it is built on: the member's base, or the branch
// the session builds it on. Nobody else has its commits, so they can be
// rewritten. One that is published has its remote copy and then its base merged
// into it instead, which are fast-forwards where they can be, so that pushing it
// never needs force. No branch of a source repo but the member's own moves.
//
// A branch built on one that has since merged moves to the member's base. A
// member whose work has been merged is left as it is, and so is one whose remote
// branch was deleted, or whose history or remote copy's was rewritten, since
// merging would bring back what the rewrite dropped.
//
// Members are independent. One with uncommitted work is skipped. A rebase or
// merge that conflicts is aborted: an agent may be working in the session, and
// leaving its worktree part way would pull it out from under it. A failed fetch
// only warns, and the refs on disk are used.
func (c *Core) Update(s *Session, opts UpdateOptions) (UpdateResult, error) {
	res := UpdateResult{Package: s.Pkg.Name, Session: s.Name}
	if err := gitx.CheckVersion(); err != nil {
		return res, err
	}
	if err := checkActive(s, opts.Members); err != nil {
		return res, err
	}
	var members []manifest.Member
	var srcs []string
	for _, mem := range s.Pkg.M.Members {
		if mem.Mode != manifest.ModeActive || (len(opts.Members) > 0 && !slices.Contains(opts.Members, mem.Name)) {
			continue
		}
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return res, err
		}
		members, srcs = append(members, mem), append(srcs, src)
	}
	runner := &apply.Runner{Events: c.Events}
	if !opts.NoFetch && len(srcs) > 0 {
		if err := runner.Run([]apply.Step{{Name: "fetch", Run: func(warn func(string)) error {
			fetchSources(srcs, warn)
			return nil
		}}}); err != nil {
			return res, err
		}
	}

	var errs []error
	incomplete := false
	for i, mem := range members {
		u := &updating{MemberUpdate: MemberUpdate{Name: mem.Name, Status: UpdateReady}, src: srcs[i], wt: s.MemberDir(mem), branch: s.BranchFor(mem.Name)}
		planUpdate(s, mem, u)
		if u.Status == UpdateReady && !opts.DryRun {
			step := apply.Step{Member: mem.Name, Name: "rebase onto " + u.Onto, Run: func(func(string)) error { return u.rebase() }}
			if u.Merge {
				step.Name, step.Run = "merge "+u.Onto, func(func(string)) error { return u.merge() }
			}
			if err := runner.Run([]apply.Step{step}); err != nil && u.Status == UpdateFailed {
				errs = append(errs, err)
			}
			if u.Status == UpdateDone {
				if err := u.record(s); err != nil {
					errs = append(errs, err)
				}
			}
		} else if u.Status == UpdateSkipped {
			c.warnEvent(mem.Name, "update", "skipped: "+u.Reason)
		}
		incomplete = incomplete || u.Status == UpdateSkipped || u.Status == UpdateConflict
		res.Members = append(res.Members, u.MemberUpdate)
	}
	if incomplete {
		errs = append(errs, &UpdateIncompleteError{Result: res})
	}
	return res, errors.Join(errs...)
}

// planUpdate decides, without changing anything, what updating one member
// would do. Uncommitted work only matters when there is something to update
// to.
func planUpdate(s *Session, mem manifest.Member, u *updating) {
	if _, err := os.Stat(u.wt); err != nil {
		u.skip("its worktree is missing; run `vmono sync`")
		return
	}
	p := checkWorktree(s, mem, u.wt, "update")
	if p != nil && !p.uncommitted {
		u.skip("%s", p.reason)
		return
	}
	refs := gitx.LiveRefs(u.src)
	base, err := baseOf(refs, s, mem, u.src, nil)
	if err != nil {
		u.skip("%v", err)
		return
	}
	bases := baseBranches(s, mem, u.src)
	pub := publicationOf(refs, u.src, u.branch, bases)
	if pub.remote != "" {
		if how, into := mergedIntoAny(u.src, "refs/heads/"+u.branch, base.refs()); how != "" {
			u.Status, u.Reason = UpdateMerged, mergedReason(u.src, how, into)
			return
		}
	}
	if pub.deleted() {
		u.skip("%s", deletedReason(pub, u.branch))
		return
	}
	if !u.planBase(s, mem, base, bases) {
		return
	}
	u.target, u.Onto = base.ref, displayRef(u.src, base.ref)
	if u.retarget {
		u.target, u.Onto = base.manifest, displayRef(u.src, base.manifest)
	}
	if u.Commits, err = gitx.RevCount(u.wt, append([]string{"HEAD", "--not"}, base.refs()...)...); err != nil {
		u.skip("%v", err)
		return
	}
	incoming := []string{u.target}
	if pub.published() {
		if !u.planMerge(pub, base) {
			return
		}
		incoming = u.merges
	}
	if len(incoming) > 0 {
		if u.Behind, err = gitx.RevCount(u.wt, append(slices.Clone(incoming), "--not", "HEAD")...); err != nil {
			u.skip("cannot compare it with %s: %v", u.Onto, err)
			return
		}
	}
	if u.Behind == 0 && !u.retarget {
		u.Status = UpdateCurrent
		return
	}
	if p != nil {
		u.skip("%s", p.reason)
		return
	}
	if u.tip, err = gitx.ResolveRef(u.src, u.target); err != nil {
		u.skip("%v", err)
		return
	}
	if u.Merge {
		return
	}
	if u.fork == "" {
		if u.fork, err = forkPoint(u.wt, base.manifest, u.target); err != nil {
			u.skip("%v", err)
			return
		}
	}
	if u.Commits, err = gitx.RevCount(u.wt, "HEAD", "--not", u.fork); err != nil {
		u.skip("%v", err)
	}
}

// planBase decides, for a branch the session builds on another, where its own
// commits begin and whether it moves to the member's own base because the other
// has merged. It is false when the member has to be skipped.
func (u *updating) planBase(s *Session, mem manifest.Member, base memberBase, bases []string) bool {
	rec := s.S.Members[mem.Name]
	if rec.From == "" {
		return true
	}
	if rec.BaseTip != "" && gitx.IsMerged(u.wt, rec.BaseTip, "HEAD") {
		u.fork = rec.BaseTip
	}
	if base.lost != "" {
		// What it was built on can only be told by the commit it was at.
		if rec.BaseTip == "" || mergedInto(u.src, rec.BaseTip, base.manifest) == "" {
			u.skip("what it is built on, %s, is gone and does not look merged into %s; rebase it onto what it should be built on by hand", base.lost, displayRef(u.src, base.manifest))
			return false
		}
		u.retarget = true
	} else if pub := publicationOf(gitx.LiveRefs(u.src), u.src, bases[len(bases)-1], bases[:len(bases)-1]); pub.remote != "" {
		u.retarget = mergedInto(u.src, base.ref, base.manifest) != ""
	}
	if u.retarget {
		u.Notes = append(u.Notes, fmt.Sprintf("%s has merged, so it is now built on %s", rec.From, displayRef(u.src, base.manifest)))
	}
	return true
}

// planMerge plans the merges a published branch is updated with: its remote
// copy and then its base, each only when it has something new. It is false when
// the member has to be skipped, because its history or the remote copy's was
// rewritten, or the remote branch is not a copy of it at all.
func (u *updating) planMerge(pub publication, base memberBase) bool {
	u.Merge = true
	remote := shortRef(pub.ref)
	moves := gitx.Reflog(u.src, pub.ref)
	for _, m := range moves {
		if gitx.IsMerged(u.src, m.Commit, "refs/heads/"+u.branch) && !gitx.IsMerged(u.src, m.Commit, pub.ref) {
			u.skip("%s was rewritten since this branch had it, and merging would bring the old commits back beside the new; compare them, then run `git reset --hard %s` or rebase onto it in the member", remote, remote)
			return false
		}
		if m.Pushed() && gitx.IsMerged(u.src, m.Commit, pub.ref) && !gitx.IsMerged(u.src, m.Commit, "refs/heads/"+u.branch) {
			u.skip("this branch was rewritten after it was pushed, and merging %s would bring the old commits back beside the new; if the rewrite was meant, run `git push --force-with-lease` in the member", remote)
			return false
		}
	}
	if !pub.upstream {
		shared, err := gitx.RevCount(u.src, append([]string{"refs/heads/" + u.branch, "--not", pub.ref}, base.refs()...)...)
		if err != nil || shared == u.Commits {
			u.skip("%s has a branch of the same name that shares no work with this one; rename this one with `git branch -m`, or track the other if it is meant to be the same", pub.remote)
			return false
		}
		u.track = remote
	}
	for _, ref := range []string{pub.ref, u.target} {
		if !gitx.IsMerged(u.wt, ref, "HEAD") {
			u.merges = append(u.merges, ref)
		}
	}
	if len(u.merges) > 0 {
		var names []string
		for _, ref := range u.merges {
			names = append(names, displayRef(u.src, ref))
		}
		u.Onto = strings.Join(names, " and ")
	}
	return true
}

// record saves what an update changed about what the branch is built on, and
// makes a published branch track its remote copy.
func (u *updating) record(s *Session) error {
	if u.track != "" {
		if err := gitx.SetUpstream(u.src, u.branch, u.track); err != nil {
			return err
		}
	}
	rec := s.S.Members[u.Name]
	if rec.From == "" {
		return nil
	}
	rec.BaseTip = u.tip
	if u.retarget {
		rec.From, rec.BaseTip = "", ""
	}
	s.S.SetMember(u.Name, rec)
	return manifest.SaveSession(s.Dir, s.S)
}

// worktreeProblem is why a member's worktree cannot be rebased as it stands.
type worktreeProblem struct {
	reason string
	// conflicts lists the files a rebase already under way is stopped on.
	conflicts []string
	// uncommitted is true when uncommitted work is all that is in the way.
	uncommitted bool
}

// checkWorktree returns what keeps the member worktree at wt from being
// updated, or nil when it is clean and on its branch in the session. then is the
// command the reason tells the user to run again.
func checkWorktree(s *Session, mem manifest.Member, wt, then string) *worktreeProblem {
	if gitx.RebaseInProgress(wt) {
		if files, _ := gitx.ConflictedFiles(wt); len(files) > 0 {
			return &worktreeProblem{conflicts: files, reason: "a rebase is stopped on conflicts; resolve them, `git add`, `git rebase --continue`, then " + then + " again"}
		}
		return &worktreeProblem{reason: fmt.Sprintf("a rebase is in progress; run `git rebase --continue` in %s, then %s again", wt, then)}
	}
	st, err := gitx.StatusOf(wt)
	switch {
	case err != nil:
		return &worktreeProblem{reason: fmt.Sprintf("cannot read its state: %v", err)}
	case st.Detached || st.Branch != s.BranchFor(mem.Name):
		return &worktreeProblem{reason: fmt.Sprintf("its worktree is on %s, not the session branch %s", firstNonEmpty(st.Branch, "a detached HEAD"), s.BranchFor(mem.Name))}
	case st.Dirty > 0 || st.Untracked > 0:
		return &worktreeProblem{uncommitted: true, reason: fmt.Sprintf("uncommitted changes (%d modified, %d untracked); commit them first", st.Dirty, st.Untracked)}
	}
	return nil
}

// forkPoint returns the commit after which a session's own commits begin: the
// later of its merge-base with the member's base and with the target.
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

// rebase runs the planned update. A rebase that stops is aborted, which puts
// the member back where it was.
func (u *updating) rebase() error {
	err := gitx.RebaseOnto(u.wt, u.target, u.fork)
	if err == nil {
		u.Status = UpdateDone
		return nil
	}
	u.Status, u.Reason = UpdateFailed, oneLine(err.Error())
	if !gitx.RebaseInProgress(u.wt) {
		return err
	}
	files, _ := gitx.ConflictedFiles(u.wt)
	if aerr := gitx.RebaseAbort(u.wt); aerr != nil {
		u.Reason = fmt.Sprintf("rebasing onto %s stopped and could not be aborted: %v", u.Onto, aerr)
		return errors.New(u.Reason)
	}
	if len(files) == 0 {
		return err
	}
	u.Status, u.Conflicts = UpdateConflict, files
	u.Reason = fmt.Sprintf("rebasing onto %s conflicts in %s; the rebase was aborted and nothing changed, run `git rebase --onto %s %s` in the member to resolve it", u.Onto, strings.Join(files, ", "), u.Onto, u.fork)
	return errors.New(u.Reason)
}

// merge merges the planned refs into the branch, in order. A merge that stops is
// aborted, and one already made before it is undone, which puts the member back
// where it was.
func (u *updating) merge() error {
	tip, err := gitx.ResolveRef(u.wt, "HEAD")
	if err != nil {
		u.Status, u.Reason = UpdateFailed, err.Error()
		return err
	}
	for _, ref := range u.merges {
		err := gitx.Merge(u.wt, ref)
		if err == nil {
			continue
		}
		files, _ := gitx.ConflictedFiles(u.wt)
		u.Status, u.Reason = UpdateFailed, oneLine(err.Error())
		_ = gitx.MergeAbort(u.wt)
		if rerr := gitx.ResetKeep(u.wt, tip); rerr != nil {
			u.Reason = fmt.Sprintf("merging %s stopped and could not be undone: %v", displayRef(u.src, ref), rerr)
		} else if len(files) > 0 {
			var cmds []string
			for _, r := range u.merges {
				cmds = append(cmds, "`git merge "+displayRef(u.src, r)+"`")
			}
			u.Status, u.Conflicts = UpdateConflict, files
			u.Reason = fmt.Sprintf("merging %s conflicts in %s; the merge was aborted and nothing changed, run %s in the member to resolve it", displayRef(u.src, ref), strings.Join(files, ", "), strings.Join(cmds, " and then "))
		}
		return errors.New(u.Reason)
	}
	u.Status = UpdateDone
	return nil
}

// shortRef drops the namespace from a full ref name, for display.
func shortRef(ref string) string {
	for _, p := range []string{"refs/heads/", "refs/remotes/"} {
		if s, ok := strings.CutPrefix(ref, p); ok {
			return s
		}
	}
	return ref
}

// checkActive rejects a name that is not an active member of the session's
// package.
func checkActive(s *Session, names []string) error {
	for _, name := range names {
		if !slices.ContainsFunc(s.Pkg.M.Members, func(m manifest.Member) bool {
			return m.Name == name && m.Mode == manifest.ModeActive
		}) {
			return fmt.Errorf("%q is not an active member of %s", name, s.Pkg.Name)
		}
	}
	return nil
}
