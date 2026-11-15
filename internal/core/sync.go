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

// reservedTop are top-level names in a package that are not members.
var reservedTop = map[string]bool{
	".git": true, ".vmono": true, ".claude": true, ".opencode": true,
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

// Sync makes the package directory match its manifest and regenerates
// files. It recreates missing worktrees (pruning stale registrations
// first), warns about directories the manifest does not list, never
// removes anything, and, with Refs, moves clean reference members to their
// latest fetched ref. Per-member problems are reported in Skipped and as
// warnings; the rest of the sync still runs.
func (c *Core) Sync(p *Package, opts SyncOptions) (SyncResult, error) {
	res := SyncResult{Recreated: []string{}, Unlisted: []string{}, RefsMoved: []string{}, Skipped: []string{}}
	var errs []error

	if err := c.prefetchForSync(p, opts); err != nil {
		return res, err
	}
	for _, mem := range p.M.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return res, err
		}
		dir := p.MemberDir(mem)
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
			if err := c.recreateMember(p, mem, src); err != nil {
				res.Skipped = append(res.Skipped, mem.Name+": "+err.Error())
				errs = append(errs, err)
				continue
			}
			res.Recreated = append(res.Recreated, mem.Name)
		case wtOK:
			if opts.Refs && mem.Mode == manifest.ModeReference {
				moved, skip := c.moveRef(p, mem, src)
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

	res.Unlisted = unlistedDirs(p)
	for _, u := range res.Unlisted {
		c.warnEvent("", "sync", "directory not in manifest: "+u)
	}

	changed, err := c.Render(p)
	if err != nil {
		return res, err
	}
	res.Files = append([]string{}, changed...)
	if c.Cfg.AutoCommit {
		if _, err := gitx.CommitAll(p.Dir, fmt.Sprintf("vmono: sync (%s)", c.Author), ""); err != nil {
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
func (c *Core) prefetchForSync(p *Package, opts SyncOptions) error {
	var srcs []string
	for _, mem := range p.M.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return err
		}
		if _, err := os.Stat(src); err != nil {
			continue
		}
		switch worktreeOf(src, p.MemberDir(mem)) {
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
func (c *Core) recreateMember(p *Package, mem manifest.Member, src string) error {
	if err := gitx.WorktreePrune(src); err != nil {
		return err
	}
	exists := false
	if mem.Mode == manifest.ModeActive {
		if at, err := gitx.BranchCheckedOutAt(src, p.M.Branch); err != nil {
			return err
		} else if at != "" {
			return fmt.Errorf("branch %q is already checked out at %s", p.M.Branch, at)
		}
		exists = gitx.BranchExists(src, p.M.Branch)
	}
	return (&apply.Runner{Events: c.Events}).Run(c.addMemberSteps(p, mem, src, exists))
}

// moveRef moves a clean reference member to its latest fetched ref, using
// the refs prefetchForSync already brought in. It returns whether it moved
// and, if it declined, why.
func (c *Core) moveRef(p *Package, mem manifest.Member, src string) (moved bool, skip string) {
	dir := p.MemberDir(mem)
	st, err := gitx.StatusOf(dir)
	if err != nil {
		return false, err.Error()
	}
	if st.Dirty > 0 || st.Untracked > 0 {
		return false, "reference member has local changes; left alone"
	}
	ref, err := resolveRef(src, mem.Ref, nil)
	if err != nil {
		return false, err.Error()
	}
	target, err := gitx.ResolveRef(src, ref)
	if err != nil {
		return false, err.Error()
	}
	head, err := gitx.ResolveRef(dir, "HEAD")
	if err != nil {
		return false, err.Error()
	}
	if head == target {
		return false, ""
	}
	if err := gitx.CheckoutDetached(dir, target); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// unlistedDirs returns top-level (and _ref) directories that are neither
// reserved files nor listed by a member.
func unlistedDirs(p *Package) []string {
	listed := map[string]bool{}
	for _, mem := range p.M.Members {
		slash := filepath.ToSlash(mem.Path)
		listed[slash] = true
		listed[strings.SplitN(slash, "/", 2)[0]] = true
	}
	out := []string{}
	scan := func(rel string) {
		entries, err := os.ReadDir(filepath.Join(p.Dir, filepath.FromSlash(rel)))
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
