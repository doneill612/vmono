package core

import (
	"fmt"
	"os"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// MemberSafety is the result of the pre-delete check for one member: work
// that removing its worktree would discard.
type MemberSafety struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Missing   bool   `json:"missing,omitempty"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	// Unpushed counts commits that exist on no remote.
	Unpushed int    `json:"unpushed"`
	Error    string `json:"error,omitempty"`
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
			parts = append(parts, fmt.Sprintf("%s (cannot inspect: %s)", s.Name, s.Error))
		default:
			parts = append(parts, fmt.Sprintf("%s (%d dirty, %d untracked, %d unpushed)", s.Name, s.Dirty, s.Untracked, s.Unpushed))
		}
	}
	return ErrRefused.Error() + ": " + strings.Join(parts, "; ") + " (use --force to override)"
}

// Is makes errors.Is(err, ErrRefused) true.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Safety checks the given members for work that would be lost if their
// worktrees were removed: dirty files, untracked files, and commits on no
// remote. Members whose directory is already gone are safe.
func (c *Core) Safety(p *Package, members []manifest.Member) []MemberSafety {
	out := make([]MemberSafety, 0, len(members))
	for _, mem := range members {
		s := MemberSafety{Name: mem.Name, Mode: mem.Mode}
		dir := p.MemberDir(mem)
		if _, err := os.Stat(dir); err != nil {
			s.Missing = true
			out = append(out, s)
			continue
		}
		st, err := gitx.StatusOf(dir)
		if err != nil {
			s.Error = err.Error()
			out = append(out, s)
			continue
		}
		s.Dirty, s.Untracked = st.Dirty, st.Untracked
		if n, err := gitx.Unpushed(dir); err != nil {
			s.Error = err.Error()
		} else {
			s.Unpushed = n
		}
		out = append(out, s)
	}
	return out
}

// SafetyCheck runs Safety over every member of the package.
func (c *Core) SafetyCheck(p *Package) []MemberSafety {
	return c.Safety(p, p.M.Members)
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

// DeleteOptions tunes Delete.
type DeleteOptions struct {
	// Force deletes even when members have dirty files, untracked files or
	// unpushed commits.
	Force bool
	// DeleteBranches deletes package branches that are already merged into
	// their base. Unmerged branches are always kept, even with Force.
	DeleteBranches bool
}

// DeleteResult reports what happened to package branches.
type DeleteResult struct {
	BranchesDeleted []string `json:"branches_deleted"`
	BranchesKept    []string `json:"branches_kept"`
}

// Delete tears a package down: it checks every member for lost work
// (returning a *RefusedError unless forced), removes each worktree and
// prunes its source, optionally deletes merged branches, then removes the
// package directory and index entry. Source repos' working trees and
// checked-out branches are never touched.
func (c *Core) Delete(p *Package, opts DeleteOptions) (DeleteResult, error) {
	var res DeleteResult
	if bad := unsafeOf(c.SafetyCheck(p)); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}

	var steps []apply.Step
	for _, mem := range p.M.Members {
		mem := mem
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return res, err
		}
		dir := p.MemberDir(mem)
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree remove", Run: func(warn func(string)) error {
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
		if opts.DeleteBranches && mem.Mode == manifest.ModeActive {
			steps = append(steps, apply.Step{Member: mem.Name, Name: "delete branch", Run: func(warn func(string)) error {
				kept, deleted := deleteMergedBranch(src, p.M.Branch, mem.Base)
				if deleted {
					res.BranchesDeleted = append(res.BranchesDeleted, mem.Name+":"+p.M.Branch)
				} else if kept != "" {
					res.BranchesKept = append(res.BranchesKept, mem.Name+":"+p.M.Branch)
					warn("kept branch " + p.M.Branch + ": " + kept)
				}
				return nil
			}})
		}
	}
	steps = append(steps, apply.Step{Name: "remove package", Run: func(func(string)) error {
		return os.RemoveAll(p.Dir)
	}})

	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	c.Idx.RemovePackage(p.Name)
	_ = c.Idx.Save()
	return res, nil
}

// deleteMergedBranch deletes branch from src only when it is fully merged
// into base. It returns why a branch was kept, or "" if it did not exist.
func deleteMergedBranch(src, branch, base string) (keptReason string, deleted bool) {
	if !gitx.BranchExists(src, branch) {
		return "", false
	}
	ref, _ := gitx.ResolveDefaultRef(src, base)
	if !gitx.RefExists(src, ref) || !gitx.IsMerged(src, branch, ref) {
		return "not merged into " + ref, false
	}
	if err := gitx.DeleteBranch(src, branch, true); err != nil {
		return err.Error(), false
	}
	return "", true
}
