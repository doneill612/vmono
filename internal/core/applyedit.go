package core

import (
	"bytes"
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

// EditPlan is what applying an edited manifest would do, to every session of
// the package. The TUI shows it for confirmation; Unsafe lists removals, per
// session, that would discard work.
type EditPlan struct {
	Plan   manifest.Plan
	Unsafe []MemberSafety
	// Sessions are the sessions the plan applies to.
	Sessions []*Session
	// Next is the normalized manifest that would be saved.
	Next *manifest.Manifest
	// Srcs maps each member name in Next to its absolute source.
	Srcs map[string]string
}

// PlanEdit normalizes and validates an edited manifest against the current
// one and returns the diff, without changing anything. The package name and
// creation time cannot change.
func (c *Core) PlanEdit(p *Package, edited *manifest.Manifest) (*EditPlan, error) {
	next := clone(edited)
	if next.Name != p.Name {
		return nil, fmt.Errorf("package names cannot change (was %q, got %q): session history is keyed by path", p.Name, next.Name)
	}
	next.Created = p.M.Created
	srcs, err := c.prepareMembers(next)
	if err != nil {
		return nil, err
	}
	if err := next.Validate(); err != nil {
		return nil, err
	}
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return nil, err
	}
	plan := manifest.Diff(p.M, next)
	unsafe := unsafeOf(c.safetyAcross(sessions, plan.Removals()))
	return &EditPlan{Plan: plan, Unsafe: unsafe, Sessions: sessions, Next: next, Srcs: srcs}, nil
}

// ApplyEdit applies an edited manifest as a diff: only members added,
// removed or switched run, in every session, and the rest are left alone.
// Removals that would discard work in any session return a *RefusedError
// unless forced.
//
// Order: additions that do not reuse a removed path run first, in every
// session, and are undone if one fails, then removals, then additions that
// replace a removed path (a changed source, base or ref). A failure in the
// last group leaves the removed worktrees gone; the saved manifest is
// unchanged, so a following `vmono sync` rebuilds them.
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
	for _, s := range ep.Sessions {
		for _, r := range removals {
			if err := checkNotInside(s.MemberDir(r)); err != nil {
				return plan, err
			}
		}
	}

	removing := map[string]bool{}
	removedPaths := map[string]bool{}
	for _, s := range ep.Sessions {
		for _, r := range removals {
			removing[realPath(filepath.Join(s.Dir, filepath.FromSlash(r.Path)))] = true
		}
	}
	for _, r := range removals {
		removedPaths[strings.ToLower(filepath.ToSlash(r.Path))] = true
	}
	// A re-added active member whose old worktree holds the session branch
	// is a replacement, so reusing the branch is expected, not an error.
	replacing := map[string]bool{}
	for _, r := range plan.Remove {
		replacing[r.Name] = r.Mode == manifest.ModeActive
	}
	existing := make(map[string]map[string]bool, len(ep.Sessions))
	for _, s := range ep.Sessions {
		ex, err := preflightMembers(ep.Next, s.BranchFor, ep.Srcs, adds, always(opts.ReuseBranch || len(replacing) > 0), removing)
		if err != nil {
			return plan, sessionErr(s, len(ep.Sessions), err)
		}
		existing[s.Name] = ex
		for _, mem := range adds {
			if !replacing[mem.Name] && ex[mem.Name] && !opts.ReuseBranch {
				return plan, &BranchExistsError{Member: mem.Name, Branch: s.BranchFor(mem.Name)}
			}
		}
	}

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
	multi := len(ep.Sessions) > 1
	addSteps := func(list []manifest.Member, fetch bool) []apply.Step {
		var steps []apply.Step
		if fetch && len(ep.Sessions) > 0 {
			steps = c.fetchStep(addSrcs...)
		}
		for _, s := range ep.Sessions {
			for _, mem := range list {
				ss := c.addMemberSteps(s, mem, ep.Srcs[mem.Name], branchPlan{exists: existing[s.Name][mem.Name]})
				if multi {
					labelSteps(ss, s.Name)
				}
				steps = append(steps, ss...)
			}
		}
		return steps
	}

	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(addSteps(first, true)); err != nil {
		return plan, err
	}
	var rmSteps []apply.Step
	var res DeleteResult
	for _, s := range ep.Sessions {
		for _, mem := range removals {
			ss, err := c.removeMemberSteps(s, mem, opts.Force, false, &res)
			if err != nil {
				return plan, err
			}
			if multi {
				labelSteps(ss, s.Name)
			}
			rmSteps = append(rmSteps, ss...)
		}
	}
	if err := (&apply.Runner{Events: c.Events}).Run(rmSteps); err != nil {
		return plan, err
	}
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(addSteps(last, false)); err != nil {
		return plan, err
	}

	if err := recordCreated(ep.Sessions, adds, existing); err != nil {
		return plan, err
	}
	for _, mem := range removals {
		if now, ok := ep.Next.Member(mem.Name); !ok || now.Mode != manifest.ModeActive {
			if err := forgetMember(ep.Sessions, mem.Name); err != nil {
				return plan, err
			}
		}
	}

	p.M = ep.Next
	return plan, c.save(p, "edit")
}
