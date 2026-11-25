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
	// Onto is the branch the member is updated to, such as "origin/main".
	Onto   string `json:"onto,omitempty"`
	Status string `json:"status"`
	// Behind counts the commits on Onto the session lacked.
	Behind int `json:"behind"`
	// Commits counts the session's own commits, replayed on top of Onto.
	Commits int `json:"commits"`
	// Reason explains a skipped, conflicted or failed member.
	Reason string `json:"reason,omitempty"`
	// Conflicts lists the files an aborted rebase conflicted in.
	Conflicts []string `json:"conflicts,omitempty"`
	// Merge is true when the member is updated by merging instead of rebasing,
	// because its landing branch and the remote copy have diverged.
	Merge bool `json:"merge,omitempty"`
}

// UpdateOptions tunes Update.
type UpdateOptions struct {
	// Members names the active members to update. Empty means every one.
	Members []string
	// Merge also updates members whose landing branch has diverged from its
	// copy on the remote, by merging both into the session branch.
	Merge bool
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
// run: the public MemberUpdate plus the refs and paths the rebase needs.
type updating struct {
	MemberUpdate
	src, wt, target, fork string
	// merges are the refs a diverged member merges, in order.
	merges []string
}

// skip marks the member as left alone.
func (u *updating) skip(format string, args ...any) {
	u.Status, u.Reason = UpdateSkipped, fmt.Sprintf(format, args...)
}

// Update brings each active member of the session up to date with the branch
// its work lands on. The sources are fetched, and the member's own commits are
// then rebased, inside the session's worktree, onto the landing branch or its
// copy on the remote, whichever is further ahead, or onto the member's base
// when the landing branch does not exist yet. No source repo branch moves.
//
// Members are independent. One with uncommitted work is skipped, and so is one
// whose landing branch has diverged from its remote copy, since rebasing onto
// either side could take commits the session already has out of it. With
// opts.Merge both sides are merged into such a session instead: it then holds
// everything, so finishing fast-forwards the landing branch, and pushing that
// is a fast-forward too. The merge commit lands with the work, which is why it
// has to be asked for. A rebase or merge that conflicts is aborted, unlike in Finish: an agent may be working in the
// session, and leaving its worktree mid-rebase would pull it out from under
// it. A failed fetch only warns, and the refs on disk are used.
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
		u := &updating{MemberUpdate: MemberUpdate{Name: mem.Name, Status: UpdateReady}, src: srcs[i], wt: s.MemberDir(mem)}
		planUpdate(s, mem, opts.Merge, u)
		if u.Status == UpdateReady && !opts.DryRun {
			step := apply.Step{Member: mem.Name, Name: "rebase onto " + u.Onto, Run: func(func(string)) error { return u.rebase() }}
			if u.Merge {
				step.Name, step.Run = "merge "+u.Onto, func(func(string)) error { return u.merge() }
			}
			if err := runner.Run([]apply.Step{step}); err != nil && u.Status == UpdateFailed {
				errs = append(errs, err)
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
func planUpdate(s *Session, mem manifest.Member, merge bool, u *updating) {
	if _, err := os.Stat(u.wt); err != nil {
		u.skip("its worktree is missing; run `vmono sync`")
		return
	}
	p := checkWorktree(s, mem, u.wt, "update")
	if p != nil && !p.uncommitted {
		u.skip("%s", p.reason)
		return
	}
	base, err := resolveRef(u.src, mem.Base, nil)
	if err != nil {
		u.skip("%v", err)
		return
	}
	u.target = base
	into, _ := landingBranch(s, mem, u.src)
	switch {
	case into == "":
	case gitx.BranchExists(u.src, into):
		u.target, u.Merge = freshest(u.src, into)
	default:
		if ref := remoteCounterpart(u.src, into); ref != "" {
			u.target = ref
		}
	}
	u.Onto = shortRef(u.target)
	incoming := []string{u.target}
	if u.Merge {
		u.merges = []string{u.target, remoteCounterpart(u.src, into)}
		u.Onto += " and " + shortRef(u.merges[1])
		incoming = u.merges
	}
	if u.Behind, err = gitx.RevCount(u.wt, append(slices.Clone(incoming), "--not", "HEAD")...); err != nil {
		u.skip("cannot compare it with %s: %v", u.Onto, err)
		return
	}
	if u.Behind == 0 {
		u.Status = UpdateCurrent
		return
	}
	if p != nil {
		u.skip("%s", p.reason)
		return
	}
	if u.Merge {
		if !merge {
			u.Merge = false
			u.skip("%s have diverged; `vmono session update --merge` merges both into the session, or reconcile them in %s", u.Onto, u.src)
			return
		}
		u.Commits, err = gitx.RevCount(u.wt, append([]string{"HEAD", "--not", base}, u.merges...)...)
		if err != nil {
			u.skip("%v", err)
		}
		return
	}
	if u.fork, err = forkPoint(u.wt, base, u.target); err == nil {
		u.Commits, err = gitx.RevCount(u.wt, "HEAD", "--not", u.fork)
	}
	if err != nil {
		u.skip("%v", err)
	}
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

// merge merges both sides of a diverged landing branch into the session. A
// merge that stops is aborted, and one already made before it is undone, which
// puts the member back where it was.
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
			u.Reason = fmt.Sprintf("merging %s stopped and could not be undone: %v", shortRef(ref), rerr)
		} else if len(files) > 0 {
			u.Status, u.Conflicts = UpdateConflict, files
			u.Reason = fmt.Sprintf("merging %s conflicts in %s; the merge was aborted and nothing changed, run `git merge %s` and then `git merge %s` in the member to resolve it", shortRef(ref), strings.Join(files, ", "), shortRef(u.merges[0]), shortRef(u.merges[1]))
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
