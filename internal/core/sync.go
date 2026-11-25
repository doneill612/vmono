package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/render"
)

// SyncOptions tunes Sync.
type SyncOptions struct {
	// Refs also moves clean reference members to their latest fetched ref.
	Refs bool
}

// SyncResult reports what Sync did.
type SyncResult struct {
	// Recreated are members whose missing worktree was rebuilt.
	Recreated []string `json:"recreated"`
	// Unlisted are directories in the package the manifest does not list.
	Unlisted []string `json:"unlisted"`
	// RefsMoved are reference members moved to a newer ref.
	RefsMoved []string `json:"refs_moved"`
	// Skipped are members that could not be synced, with reasons.
	Skipped []string `json:"skipped"`
	// Files are the generated files that were rewritten.
	Files []string `json:"files"`
}

// reservedTop are top-level names in a session that are not members.
var reservedTop = map[string]bool{
	".git": true, ".vmono": true, ".claude": true, ".opencode": true, ".agents": true,
	"AGENTS.md": true, "CLAUDE.md": true, ".gitignore": true, "opencode.json": true,
}

// warnEvent emits a package-level warning on the event channel.
func (c *Core) warnEvent(member, step, msg string) {
	if c.Events != nil {
		c.Events <- apply.Event{Member: member, Step: step, Status: apply.StatusWarn, Message: msg}
	}
}

// worktreeState classifies a member's directory against its source.
type worktreeState int

const (
	wtOK worktreeState = iota
	wtMissing
	wtForeign // exists but is not a worktree of the member's source
)

// worktreeOf reports whether dir is a worktree of the source repo.
func worktreeOf(src, dir string) worktreeState {
	if _, err := os.Stat(dir); err != nil {
		return wtMissing
	}
	have, err := gitx.CommonDir(dir)
	if err != nil {
		return wtForeign
	}
	want, err := gitx.CommonDir(src)
	if err != nil || realPath(have) != realPath(want) {
		return wtForeign
	}
	return wtOK
}

// SessionSync is one session's result in a package-wide sync.
type SessionSync struct {
	Session string `json:"session"`
	SyncResult
}

// SyncPackage syncs every session of a package: it regenerates the package's
// own .gitignore, committing it when it changed and auto_commit is on, then
// runs Sync on each session. A session that fails does not stop the others;
// the errors are joined.
func (c *Core) SyncPackage(p *Package, opts SyncOptions) ([]SessionSync, error) {
	var errs []error
	changed, err := render.RenderPackage(p.Dir)
	if err != nil {
		errs = append(errs, err)
	} else if len(changed) > 0 && c.Cfg.AutoCommit {
		if _, err := gitx.CommitAll(p.Dir, fmt.Sprintf("vmono: sync (%s)", c.Author), ""); err != nil {
			errs = append(errs, err)
		}
	}
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	out := make([]SessionSync, 0, len(sessions))
	for _, s := range sessions {
		res, err := c.Sync(s, opts)
		out = append(out, SessionSync{Session: s.Name, SyncResult: res})
		if err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", s.Name, err))
		}
	}
	return out, errors.Join(errs...)
}

// Sync makes a session directory match its package's manifest and
// regenerates files. It recreates missing worktrees (pruning stale
// registrations first), warns about directories the manifest does not list,
// never removes anything, and, with Refs, moves clean reference members to
// their latest fetched ref. Per-member problems are reported in Skipped and
// as warnings; the rest of the sync still runs.
func (c *Core) Sync(s *Session, opts SyncOptions) (SyncResult, error) {
	res := SyncResult{Recreated: []string{}, Unlisted: []string{}, RefsMoved: []string{}, Skipped: []string{}}
	var errs []error

	if err := c.prefetchForSync(s, opts); err != nil {
		return res, err
	}
	for _, mem := range s.Pkg.M.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return res, err
		}
		dir := s.MemberDir(mem)
		if _, err := os.Stat(src); err != nil {
			msg := "source repo is missing: " + src
			res.Skipped = append(res.Skipped, mem.Name+": "+msg)
			c.warnEvent(mem.Name, "sync", msg+" (try `vmono doctor --fix`)")
			continue
		}
		switch worktreeOf(src, dir) {
		case wtForeign:
			msg := "directory exists but is not a worktree of " + src
			res.Skipped = append(res.Skipped, mem.Name+": "+msg)
			c.warnEvent(mem.Name, "sync", msg)
		case wtMissing:
			if err := c.recreateMember(s, mem, src); err != nil {
				res.Skipped = append(res.Skipped, mem.Name+": "+err.Error())
				errs = append(errs, err)
				continue
			}
			res.Recreated = append(res.Recreated, mem.Name)
		case wtOK:
			if opts.Refs && mem.Mode == manifest.ModeReference {
				moved, skip := c.moveRef(s, mem, src)
				if moved {
					res.RefsMoved = append(res.RefsMoved, mem.Name)
				}
				if skip != "" {
					res.Skipped = append(res.Skipped, mem.Name+": "+skip)
					c.warnEvent(mem.Name, "sync refs", skip)
				}
			}
		}
	}

	res.Unlisted = unlistedDirs(s)
	for _, u := range res.Unlisted {
		c.warnEvent("", "sync", "directory not in manifest: "+u)
	}

	changed, err := c.Render(s)
	if err != nil {
		return res, err
	}
	res.Files = append([]string{}, changed...)
	if c.Cfg.AutoCommit {
		if _, err := gitx.CommitAll(s.Dir, fmt.Sprintf("vmono: sync (%s)", c.Author), ""); err != nil {
			return res, err
		}
	}
	return res, errors.Join(errs...)
}

// prefetchForSync fetches, concurrently and once per repo, every source Sync
// is about to rely on: the sources of members whose worktree is missing
// (when fetch_on_create is set) and, with opts.Refs, of reference members
// whose worktree is intact. A source that is missing on disk is skipped
// here and reported by the main loop. Fetch failures only warn, so the sync
// continues with the refs already on disk.
func (c *Core) prefetchForSync(s *Session, opts SyncOptions) error {
	var srcs []string
	for _, mem := range s.Pkg.M.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return err
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		switch worktreeOf(src, s.MemberDir(mem)) {
		case wtMissing:
			if c.Cfg.FetchOnCreate {
				srcs = append(srcs, src)
			}
		case wtOK:
			if opts.Refs && mem.Mode == manifest.ModeReference {
				srcs = append(srcs, src)
			}
		}
	}
	if len(srcs) > 0 {
		fetchSources(srcs, func(msg string) { c.warnEvent("", "fetch", msg) })
	}
	return nil
}

// recreateMember rebuilds one member's missing worktree. Stale
// registrations are pruned first, and an existing package branch is checked
// out rather than recreated, since branches are kept when worktrees go.
func (c *Core) recreateMember(s *Session, mem manifest.Member, src string) error {
	if err := gitx.WorktreePrune(src); err != nil {
		return err
	}
	exists := false
	branch := s.BranchFor(mem.Name)
	if mem.Mode == manifest.ModeActive {
		if at, err := gitx.BranchCheckedOutAt(src, branch); err != nil {
			return err
		} else if at != "" {
			return fmt.Errorf("branch %q is already checked out at %s", branch, at)
		}
		exists = gitx.BranchExists(src, branch)
	}
	return (&apply.Runner{Events: c.Events}).Run(c.addMemberSteps(s, mem, src, branchPlan{exists: exists}))
}

// moveRef moves a clean reference member to its latest fetched ref, using
// the refs prefetchForSync already brought in. It returns whether it moved
// and, if it declined, why. One the session checks out at a branch whose remote
// copy was deleted goes back to the package's ref once that has the branch's
// work, and is left alone until then.
func (c *Core) moveRef(s *Session, mem manifest.Member, src string) (moved bool, skip string) {
	dir := s.MemberDir(mem)
	st, err := gitx.StatusOf(dir)
	if err != nil {
		return false, err.Error()
	}
	if st.Dirty > 0 || st.Untracked > 0 {
		return false, "reference member has local changes; left alone"
	}
	head, err := gitx.ResolveRef(dir, "HEAD")
	if err != nil {
		return false, err.Error()
	}
	ref, gone, err := refOf(s, mem, src, nil)
	if err != nil {
		return false, err.Error()
	}
	rec := s.S.Members[mem.Name]
	if gone != "" && mergedInto(src, head, ref) == "" {
		return false, fmt.Sprintf("%s is gone and does not look merged into %s; left where it is", gone, displayRef(src, ref))
	}
	if rec.Ref != "" && gone == "" && publicationOf(gitx.LiveRefs(src), src, rec.Ref, nil).deleted() {
		// Only a local branch is left of it, which nothing moves once its work
		// has merged.
		own, err := resolveRef(src, mem.Ref, nil)
		if err != nil {
			return false, err.Error()
		}
		if mergedInto(src, ref, own) != "" {
			ref, gone = own, rec.Ref
		}
	}
	if gone != "" {
		rec.Ref = ""
		c.warnEvent(mem.Name, "sync refs", fmt.Sprintf("%s has merged, so it is now at %s", gone, displayRef(src, ref)))
	}
	target, err := gitx.ResolveRef(src, ref)
	if err != nil {
		return false, err.Error()
	}
	if head != target {
		if err := gitx.CheckoutDetached(dir, target); err != nil {
			return false, err.Error()
		}
		moved = true
	}
	was := s.S.Members[mem.Name]
	if rec.BaseTip = target; rec.Ref == "" {
		rec.BaseTip = ""
	}
	if was.Ref != "" && rec != was {
		s.S.SetMember(mem.Name, rec)
		if err := manifest.SaveSession(s.Dir, s.S); err != nil {
			return moved, err.Error()
		}
	}
	return moved, ""
}

// unlistedDirs returns top-level (and _ref) directories that are neither
// reserved files nor listed by a member.
func unlistedDirs(s *Session) []string {
	listed := map[string]bool{}
	for _, mem := range s.Pkg.M.Members {
		slash := filepath.ToSlash(mem.Path)
		listed[slash] = true
		listed[strings.SplitN(slash, "/", 2)[0]] = true
	}
	out := []string{}
	scan := func(rel string) {
		entries, err := os.ReadDir(filepath.Join(s.Dir, filepath.FromSlash(rel)))
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			full := name
			if rel != "" {
				full = rel + "/" + name
			}
			if rel == "" && (reservedTop[name] || name == "_ref") {
				continue
			}
			if !e.IsDir() || listed[full] {
				continue
			}
			out = append(out, full)
		}
	}
	scan("")
	scan("_ref")
	sort.Strings(out)
	return out
}
