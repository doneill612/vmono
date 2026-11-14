package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// CreateOptions tunes Create.
type CreateOptions struct {
	// ReuseBranch checks out a package branch that already exists in a
	// source repo instead of failing. The CLI requires --reuse-branch; the
	// TUI asks and retries on BranchExistsError.
	ReuseBranch bool
}

// BranchExistsError reports that the package branch already exists in a
// source repo. Callers may retry with CreateOptions.ReuseBranch.
type BranchExistsError struct {
	Member string
	Branch string
}

// Error implements error.
func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("%s: branch %q already exists in the source repo (use --reuse-branch to check it out)", e.Member, e.Branch)
}

// prepareMember resolves one member's source to an absolute repo root,
// defaults its name and remote, and stores the source contracted. It
// returns the absolute source path. It does not apply path/base defaults,
// which need the whole manifest.
func prepareMember(mem *manifest.Member) (string, error) {
	if mem.Source == "" {
		return "", errors.New("member has no source")
	}
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return "", err
	}
	top, err := gitx.TopLevel(src)
	if err != nil {
		return "", NotFound("%s is not a git repository", src)
	}
	if bare, err := gitx.IsBare(top); err != nil || bare {
		return "", fmt.Errorf("%s is a bare repository; members must be non-bare", top)
	}
	if mem.Name == "" {
		mem.Name = manifest.Slugify(filepath.Base(top))
	}
	if mem.Remote == "" {
		mem.Remote = gitx.RemoteURL(top, "origin")
	}
	mem.Source = paths.Contract(top)
	return top, nil
}

// prepareMembers prepares every member of m and applies manifest defaults.
// It returns the absolute source path per member name. It mutates m.
func (c *Core) prepareMembers(m *manifest.Manifest) (map[string]string, error) {
	abs := map[string]string{}
	for i := range m.Members {
		top, err := prepareMember(&m.Members[i])
		if err != nil {
			return nil, fmt.Errorf("member %d: %w", i+1, err)
		}
		abs[m.Members[i].Name] = top
	}
	m.ApplyDefaults(c.Cfg.BranchPrefix)
	return abs, nil
}

// realPath cleans a path and resolves symlinks so paths git reports (which
// are resolved, e.g. /private/var on macOS) compare equal to ones vmono
// built. A path that does not exist yet is resolved through its deepest
// existing ancestor, so a missing worktree directory still compares equal
// to the registration git holds for it.
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// resolveRef returns the ref to use for a member's base or ref, falling
// back to HEAD with a warning when origin/HEAD does not exist.
func resolveRef(src, ref string, warn func(string)) (string, error) {
	resolved, fell := gitx.ResolveDefaultRef(src, ref)
	if fell && warn != nil {
		warn("no origin/HEAD; using the source's current HEAD instead")
	}
	if !gitx.RefExists(src, resolved) {
		return "", fmt.Errorf("ref %q not found in %s", resolved, src)
	}
	return resolved, nil
}

// preflightMembers checks the members in check before anything touches
// disk: sources are usable, no two active members share a repo, the package
// branch is not checked out elsewhere, and base/ref resolve. srcs maps every
// member of m (not only check) to its absolute source. It returns the
// members whose package branch already exists in their source.
func preflightMembers(m *manifest.Manifest, srcs map[string]string, check []manifest.Member, reuse bool, removing map[string]bool) (existing map[string]bool, err error) {
	existing = map[string]bool{}
	activeSources := map[string]string{}
	for _, mem := range m.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		src := srcs[mem.Name]
		if other, dup := activeSources[src]; dup {
			return nil, fmt.Errorf("members %s and %s are the same repo; one branch can only be checked out once", other, mem.Name)
		}
		activeSources[src] = mem.Name
	}
	for _, mem := range check {
		src := srcs[mem.Name]
		switch mem.Mode {
		case manifest.ModeActive:
			if at, err := gitx.BranchCheckedOutAt(src, m.Branch); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			} else if at != "" && !removing[realPath(at)] {
				return nil, fmt.Errorf("%s: branch %q is already checked out at %s", mem.Name, m.Branch, at)
			}
			if gitx.BranchExists(src, m.Branch) {
				if !reuse {
					return nil, &BranchExistsError{Member: mem.Name, Branch: m.Branch}
				}
				existing[mem.Name] = true
			} else if _, err := resolveRef(src, mem.Base, nil); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			}
		case manifest.ModeReference:
			if _, err := resolveRef(src, mem.Ref, nil); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			}
		}
	}
	return existing, nil
}

// Create builds a new package from a manifest whose members name their
// sources. It follows the spec's create sequence: validate, init the shell
// repo, add worktrees, copy files, render, commit. If any step fails, every
// step already done is undone and the error names the failing member.
//
// The manifest needs Name and Members (Source and Mode each); everything
// else is defaulted. The shell repo always gets an initial commit, even
// with auto_commit off, because OpenCode identifies a project by it.
func (c *Core) Create(m *manifest.Manifest, opts CreateOptions) (*Package, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if err := manifest.ValidateName(m.Name); err != nil {
		return nil, err
	}
	if m.Created.IsZero() {
		m.Created = time.Now().Truncate(time.Second)
	}
	srcs, err := c.prepareMembers(m)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	dir, err := c.PackageDir(m.Name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("package %q already exists at %s", m.Name, dir)
	}
	existing, err := preflightMembers(m, srcs, m.Members, opts.ReuseBranch, nil)
	if err != nil {
		return nil, err
	}

	p := &Package{Name: m.Name, Dir: dir, M: m}
	steps := []apply.Step{{
		Name: "create package",
		Run: func(func(string)) error {
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				return err
			}
			if err := os.Mkdir(dir, 0o755); err != nil {
				return err
			}
			if err := gitx.Init(dir, "main"); err != nil {
				return err
			}
			return manifest.Save(dir, m)
		},
		Undo: func() error { return os.RemoveAll(dir) },
	}}
	for _, mem := range m.Members {
		steps = append(steps, c.addMemberSteps(p, mem, srcs[mem.Name], existing[mem.Name])...)
	}
	steps = append(steps,
		apply.Step{Name: "render files", Run: func(func(string)) error {
			_, err := c.Render(p)
			return err
		}},
		apply.Step{Name: "commit", Run: func(func(string)) error {
			_, err := gitx.CommitAll(dir, "vmono: create "+m.Name, "")
			return err
		}},
	)

	runner := &apply.Runner{Events: c.Events, Rollback: true}
	if err := runner.Run(steps); err != nil {
		return nil, err
	}
	c.Idx.SetPackage(m.Name, dir)
	_ = c.Idx.Save()
	return p, nil
}

// addMemberSteps returns the apply steps that bring one member's worktree
// into existence: fetch, worktree add, and copying files. Rollback of the
// worktree step removes the worktree and any branch it created.
func (c *Core) addMemberSteps(p *Package, mem manifest.Member, src string, branchExists bool) []apply.Step {
	var steps []apply.Step
	dst := p.MemberDir(mem)
	branch := p.M.Branch

	if c.Cfg.FetchOnCreate {
		steps = append(steps, apply.Step{Member: mem.Name, Name: "fetch", Run: func(warn func(string)) error {
			if err := gitx.Fetch(src); err != nil {
				warn("fetch failed, continuing with local refs: " + err.Error())
			}
			return nil
		}})
	}

	switch mem.Mode {
	case manifest.ModeActive:
		createdBranch := false
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree add", Run: func(warn func(string)) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if branchExists {
				return gitx.WorktreeAddExisting(src, dst, branch)
			}
			base, err := resolveRef(src, mem.Base, warn)
			if err != nil {
				return err
			}
			if err := gitx.WorktreeAddBranch(src, dst, branch, base); err != nil {
				return err
			}
			createdBranch = true
			return nil
		}, Undo: func() error {
			return undoWorktree(src, dst, branch, createdBranch)
		}})
	case manifest.ModeReference:
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree add", Run: func(warn func(string)) error {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			ref, err := resolveRef(src, mem.Ref, warn)
			if err != nil {
				return err
			}
			return gitx.WorktreeAddDetached(src, dst, ref)
		}, Undo: func() error {
			return undoWorktree(src, dst, "", false)
		}})
	}

	if len(mem.Copy) > 0 {
		steps = append(steps, apply.Step{Member: mem.Name, Name: "copy files", Run: func(func(string)) error {
			_, err := copyGlobs(src, dst, mem.Copy)
			return err
		}})
	}
	return steps
}

// undoWorktree reverses a worktree add: force-remove the worktree, prune
// the source, and delete the branch only if this run created it. The forced
// removal is safe here because the worktree was created moments ago by the
// same operation.
func undoWorktree(src, dst, branch string, deleteBranch bool) error {
	var errs []error
	if _, err := os.Stat(dst); err == nil {
		if err := gitx.WorktreeRemove(src, dst, true); err != nil {
			errs = append(errs, err)
		}
	}
	if err := gitx.WorktreePrune(src); err != nil {
		errs = append(errs, err)
	}
	if deleteBranch && branch != "" && gitx.BranchExists(src, branch) {
		if err := gitx.DeleteBranch(src, branch, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
