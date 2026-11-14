package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// Finding kinds reported by Doctor.
const (
	KindBadManifest     = "bad-manifest"
	KindOrphanDir       = "orphan-dir"
	KindMissingWorktree = "missing-worktree"
	KindMovedSource     = "moved-source"
	KindMissingSource   = "missing-source"
	KindForeignDir      = "foreign-dir"
	KindStaleWorktree   = "stale-worktree"
	KindOrphanWorktree  = "orphan-worktree"
	KindIndexRebuilt    = "index"
)

// Finding is one piece of drift Doctor found.
type Finding struct {
	Kind    string `json:"kind"`
	Package string `json:"package,omitempty"`
	Member  string `json:"member,omitempty"`
	Message string `json:"message"`
	// Fixed is true when --fix repaired it (or, for the index, when it was
	// rebuilt).
	Fixed bool `json:"fixed"`
}

// DoctorResult is the outcome of a Doctor run.
type DoctorResult struct {
	Packages int       `json:"packages"`
	Findings []Finding `json:"findings"`
}

// Remaining counts findings that are still unresolved. The index rebuild is
// informational and never counts.
func (r DoctorResult) Remaining() int {
	n := 0
	for _, f := range r.Findings {
		if !f.Fixed && f.Kind != KindIndexRebuilt {
			n++
		}
	}
	return n
}

// Doctor looks for drift between manifests, worktrees and source repos:
// invalid manifests, directories with no manifest, missing worktrees,
// sources that moved, stale worktree entries and unclaimed worktrees. It
// always rebuilds the index cache from the package root. With fix it
// repairs what is safe to repair: recreating missing worktrees, relocating a
// moved source by matching its remote, and pruning stale entries.
// Directories and worktrees it cannot attribute are only reported, never
// deleted.
func (c *Core) Doctor(fix bool) (DoctorResult, error) {
	res := DoctorResult{Findings: []Finding{}}
	root, err := c.PackageRoot()
	if err != nil {
		return res, err
	}
	add := func(f Finding) { res.Findings = append(res.Findings, f) }

	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	var known []knownRepo
	if repos, rerr := c.Repos(); rerr == nil {
		for _, r := range repos {
			known = append(known, knownRepo{Path: r.Path, Remote: r.Remote})
		}
	}

	found := map[string]string{}
	sources := map[string]bool{}
	claimed := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(manifest.Path(dir)); err != nil {
			add(Finding{Kind: KindOrphanDir, Package: e.Name(), Message: "directory has no manifest; not a package (left alone)"})
			continue
		}
		p, err := c.Load(e.Name())
		if err != nil {
			add(Finding{Kind: KindBadManifest, Package: e.Name(), Message: err.Error()})
			continue
		}
		if p.M.Name != e.Name() {
			add(Finding{Kind: KindBadManifest, Package: e.Name(), Message: fmt.Sprintf("manifest name %q does not match directory", p.M.Name)})
			continue
		}
		res.Packages++
		found[p.Name] = p.Dir
		c.checkPackage(p, known, fix, sources, claimed, add)
	}

	c.checkSources(root, sources, claimed, fix, add)

	rebuilt := c.rebuildIndex(found)
	if rebuilt != "" {
		add(Finding{Kind: KindIndexRebuilt, Message: rebuilt, Fixed: true})
	}
	sort.SliceStable(res.Findings, func(i, j int) bool { return res.Findings[i].Package < res.Findings[j].Package })
	return res, nil
}

// knownRepo is a source repo that could be a moved member's new home.
type knownRepo struct{ Path, Remote string }

// checkPackage inspects one package's members, collecting the set of source
// repos it uses into sources.
func (c *Core) checkPackage(p *Package, known []knownRepo, fix bool, sources, claimed map[string]bool, add func(Finding)) {
	relocated := false
	for i := range p.M.Members {
		mem := &p.M.Members[i]
		src, err := paths.Abs(mem.Source)
		if err != nil {
			continue
		}
		if !isRepo(src) {
			newSrc, ok := findMovedSource(mem.Remote, known)
			switch {
			case !ok:
				add(Finding{Kind: KindMissingSource, Package: p.Name, Member: mem.Name,
					Message: fmt.Sprintf("source repo %s is gone and no known repo has remote %q", src, mem.Remote)})
				continue
			case !fix:
				add(Finding{Kind: KindMovedSource, Package: p.Name, Member: mem.Name,
					Message: fmt.Sprintf("source moved: %s -> %s", src, newSrc)})
				continue
			}
			mem.Source = paths.Contract(newSrc)
			src = newSrc
			relocated = true
			add(Finding{Kind: KindMovedSource, Package: p.Name, Member: mem.Name,
				Message: "source relocated to " + newSrc, Fixed: true})
		}
		sources[realPath(src)] = true
		claimed[realPath(p.MemberDir(*mem))] = true

		switch worktreeOf(src, p.MemberDir(*mem)) {
		case wtForeign:
			add(Finding{Kind: KindForeignDir, Package: p.Name, Member: mem.Name,
				Message: "directory exists but is not a worktree of " + src})
		case wtMissing:
			f := Finding{Kind: KindMissingWorktree, Package: p.Name, Member: mem.Name,
				Message: "worktree directory is missing: " + mem.Path}
			if fix {
				if err := c.recreateMember(p, *mem, src); err != nil {
					f.Message += " (could not recreate: " + err.Error() + ")"
				} else {
					f.Fixed = true
				}
			}
			add(f)
		}
	}
	if relocated {
		if err := c.save(p, "doctor relocate sources"); err != nil {
			add(Finding{Kind: KindBadManifest, Package: p.Name, Message: "could not save relocated sources: " + err.Error()})
		}
	}
}

// isRepo reports whether path is a non-bare git working tree.
func isRepo(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	top, err := gitx.TopLevel(path)
	if err != nil {
		return false
	}
	bare, err := gitx.IsBare(top)
	return err == nil && !bare
}

// findMovedSource returns the single known repo whose origin matches
// remote. An empty remote or an ambiguous match finds nothing.
func findMovedSource(remote string, known []knownRepo) (string, bool) {
	if remote == "" {
		return "", false
	}
	var match string
	for _, r := range known {
		if r.Remote == remote {
			if match != "" && match != r.Path {
				return "", false
			}
			match = r.Path
		}
	}
	return match, match != ""
}

// checkSources inspects each source repo's worktree list for entries under
// the package root. A stale entry (its directory is gone) is pruned with
// fix. A live entry no manifest claims is reported and left alone, since it
// may hold someone's work.
func (c *Core) checkSources(root string, sources, claimed map[string]bool, fix bool, add func(Finding)) {
	realRoot := realPath(root)
	var list []string
	for s := range sources {
		list = append(list, s)
	}
	sort.Strings(list)
	for _, src := range list {
		trees, err := gitx.WorktreeList(src)
		if err != nil {
			continue
		}
		var stale []Finding
		for _, wt := range trees {
			if !underDir(realPath(wt.Path), realRoot) && !underDir(filepath.Clean(wt.Path), filepath.Clean(root)) {
				continue
			}
			switch {
			case wt.Prunable:
				stale = append(stale, Finding{Kind: KindStaleWorktree,
					Message: fmt.Sprintf("%s has a stale worktree entry for %s", src, wt.Path)})
			case !claimed[realPath(wt.Path)]:
				add(Finding{Kind: KindOrphanWorktree,
					Message: fmt.Sprintf("%s has a worktree at %s that no package claims (left alone)", src, wt.Path)})
			}
		}
		pruned := len(stale) > 0 && fix && gitx.WorktreePrune(src) == nil
		for _, f := range stale {
			f.Fixed = pruned
			add(f)
		}
	}
}

// underDir reports whether p is inside dir.
func underDir(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// rebuildIndex replaces the cached package list with what is on disk,
// keeping last-opened times and launch history. It returns a description
// when it changed anything.
func (c *Core) rebuildIndex(found map[string]string) string {
	added, removed := 0, 0
	for name := range c.Idx.Packages {
		if _, ok := found[name]; !ok {
			c.Idx.RemovePackage(name)
			removed++
		}
	}
	for name, dir := range found {
		if cur, ok := c.Idx.Packages[name]; !ok || cur.Path != dir {
			c.Idx.SetPackage(name, dir)
			added++
		}
	}
	_ = c.Idx.Save()
	if added == 0 && removed == 0 {
		return ""
	}
	return fmt.Sprintf("index rebuilt: %d added or updated, %d removed", added, removed)
}
