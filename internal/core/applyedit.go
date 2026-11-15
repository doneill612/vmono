package core

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/manifest"
)

// EditOptions tunes PlanEdit and ApplyEdit.
type EditOptions struct {
	// Force allows removals that would discard unsaved work.
	Force bool
	// ReuseBranch checks out the package branch when a newly added active
	// member's source already has it.
	ReuseBranch bool
}

// EditPlan is what applying an edited manifest would do. The TUI shows it
// for confirmation; Unsafe lists removals that would discard work.
type EditPlan struct {
	Plan   manifest.Plan
	Unsafe []MemberSafety
	// Next is the normalized manifest that would be saved.
	Next *manifest.Manifest
	// Srcs maps each member name in Next to its absolute source.
	Srcs map[string]string
}

// PlanEdit normalizes and validates an edited manifest against the current
// one and returns the diff, without changing anything. The package name,
// creation time and (once active members exist) branch cannot change.
func (c *Core) PlanEdit(p *Package, edited *manifest.Manifest) (*EditPlan, error) {
	next := clone(edited)
	if next.Name != p.Name {
		return nil, fmt.Errorf("package names cannot change (was %q, got %q): session history is keyed by path", p.Name, next.Name)
	}
	next.Created = p.M.Created
	if next.Branch == "" {
		next.Branch = p.M.Branch
	}
	if next.Branch != p.M.Branch && len(p.M.Active()) > 0 {
		return nil, errors.New("the package branch cannot change while active members exist")
	}
	srcs, err := c.prepareMembers(next)
	if err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	plan := manifest.Diff(p.M, next)
	unsafe := unsafeOf(c.Safety(p, plan.Removals()))
	return &EditPlan{Plan: plan, Unsafe: unsafe, Next: next, Srcs: srcs}, nil
}

// ApplyEdit applies an edited manifest as a diff: only members added,
// removed or switched run, and the rest are left alone. Removals that would
// discard work return a *RefusedError unless forced.
//
// Order: additions that do not reuse a removed path run first and are
// undone if one fails, then removals, then additions that replace a removed
// path (a changed source, base or ref). A failure in the last group leaves
// the removed worktrees gone; the saved manifest is unchanged, so a
// following `vmono sync` rebuilds them.
func (c *Core) ApplyEdit(p *Package, edited *manifest.Manifest, opts EditOptions) (manifest.Plan, error) {
	ep, err := c.PlanEdit(p, edited)
	if err != nil {
		return manifest.Plan{}, err
	}
	plan := ep.Plan
	if plan.Empty() && bytes.Equal(manifest.Encode(p.M), manifest.Encode(ep.Next)) {
		return plan, nil
	}
	if len(ep.Unsafe) > 0 && !opts.Force {
		return plan, &RefusedError{Unsafe: ep.Unsafe}
	}

	adds := append([]manifest.Member(nil), plan.Add...)
	for _, s := range plan.Switch {
		adds = append(adds, s.New)
	}
	removals := plan.Removals()

	removing := map[string]bool{}
	removedPaths := map[string]bool{}
	for _, r := range removals {
		removing[realPath(filepath.Join(p.Dir, filepath.FromSlash(r.Path)))] = true
		removedPaths[strings.ToLower(filepath.ToSlash(r.Path))] = true
	}
	// A re-added active member whose old worktree holds the package branch
	// is a replacement, so reusing the branch is expected, not an error.
	replacing := map[string]bool{}
	for _, r := range plan.Remove {
		replacing[r.Name] = r.Mode == manifest.ModeActive
	}
	existing, err := preflightMembers(ep.Next, ep.Srcs, adds, opts.ReuseBranch || len(replacing) > 0, removing)
	if err != nil {
		return plan, err
	}
	for _, mem := range adds {
		if !replacing[mem.Name] && existing[mem.Name] && !opts.ReuseBranch {
			return plan, &BranchExistsError{Member: mem.Name, Branch: ep.Next.Branch}
		}
	}

	nextPkg := &Package{Name: p.Name, Dir: p.Dir, M: ep.Next}
	var first, last []manifest.Member
	for _, mem := range adds {
		if removedPaths[strings.ToLower(filepath.ToSlash(mem.Path))] {
			last = append(last, mem)
		} else {
			first = append(first, mem)
		}
	}
	var addSrcs []string
	for _, mem := range adds {
		addSrcs = append(addSrcs, ep.Srcs[mem.Name])
	}
	addSteps := func(list []manifest.Member, fetch bool) []apply.Step {
		var steps []apply.Step
		if fetch {
			steps = c.fetchStep(addSrcs...)
		}
		for _, mem := range list {
			steps = append(steps, c.addMemberSteps(nextPkg, mem, ep.Srcs[mem.Name], existing[mem.Name])...)
		}
		return steps
	}

	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(addSteps(first, true)); err != nil {
		return plan, err
	}
	var rmSteps []apply.Step
	var res DeleteResult
	for _, mem := range removals {
		steps, err := c.removeMemberSteps(p, mem, opts.Force, false, &res)
		if err != nil {
			return plan, err
		}
		rmSteps = append(rmSteps, steps...)
	}
	if err := (&apply.Runner{Events: c.Events}).Run(rmSteps); err != nil {
		return plan, err
	}
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(addSteps(last, false)); err != nil {
		return plan, err
	}

	p.M = ep.Next
	return plan, c.save(p, "edit")
}
