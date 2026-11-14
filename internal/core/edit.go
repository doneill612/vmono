package core

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// save is the single apply path every manifest change goes through, whether
// it comes from the TUI, the CLI or an agent: write the manifest,
// regenerate files, and commit to the shell repo as "vmono: <op>
// (<author>)" when auto_commit is on. That history records who changed what.
func (c *Core) save(p *Package, op string) error {
	if err := manifest.Save(p.Dir, p.M); err != nil {
		return err
	}
	if _, err := c.Render(p); err != nil {
		return err
	}
	if !c.Cfg.AutoCommit {
		return nil
	}
	_, err := gitx.CommitAll(p.Dir, fmt.Sprintf("vmono: %s (%s)", op, c.Author), "")
	return err
}

// clone deep-copies a manifest through its own encoding so callers can edit
// a copy and keep the original untouched if something fails.
func clone(m *manifest.Manifest) *manifest.Manifest {
	cp, err := manifest.Parse(manifest.Encode(m))
	if err != nil {
		// Encode output of a valid manifest always parses; fall back to a
		// shallow copy rather than panic.
		c := *m
		c.Members = append([]manifest.Member(nil), m.Members...)
		c.Notes = append([]manifest.Note(nil), m.Notes...)
		return &c
	}
	return cp
}

// DescribeOptions selects which free-text fields to replace. A nil field is
// left alone; a pointer to "" clears it.
type DescribeOptions struct {
	Purpose *string
	Context *string
}

// Describe replaces the purpose and/or context.
func (c *Core) Describe(p *Package, opts DescribeOptions) error {
	if opts.Purpose == nil && opts.Context == nil {
		return errors.New("nothing to change: give a purpose or a context")
	}
	var what []string
	if opts.Purpose != nil {
		p.M.Purpose = *opts.Purpose
		what = append(what, "purpose")
	}
	if opts.Context != nil {
		p.M.Context = *opts.Context
		what = append(what, "context")
	}
	return c.save(p, "describe "+strings.Join(what, "+"))
}

// SetRole sets a member's one-line role.
func (c *Core) SetRole(p *Package, member, role string) error {
	mem, ok := p.M.Member(member)
	if !ok {
		return NotFound("package %s has no member %q", p.Name, member)
	}
	mem.Role = strings.Join(strings.Fields(role), " ")
	return c.save(p, "role "+member)
}

// AddNote appends a note stamped with the time and the current author.
func (c *Core) AddNote(p *Package, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("note text is empty")
	}
	p.M.Notes = append(p.M.Notes, manifest.Note{At: time.Now().Truncate(time.Second), By: c.Author, Text: text})
	return c.save(p, "note")
}

// AddOptions describes a member to add to an existing package.
type AddOptions struct {
	// Source is a path to the repo (resolve names with ResolveRepo first).
	Source string
	// Name overrides the default member name (the repo directory name).
	Name string
	// Reference adds a read-only reference member instead of an active one.
	Reference bool
	// Role is the member's one-line role.
	Role string
	// Ref is the ref for a reference member, or Base the base for an active
	// one. Both default to origin/HEAD.
	Ref, Base string
	// Copy lists untracked-file globs to copy into the new worktree.
	Copy []string
	// ReuseBranch checks out the package branch if it already exists.
	ReuseBranch bool
}

// AddMember adds a member to a package: validate, create its worktree,
// copy files, then save the manifest and regenerate files. If the worktree
// steps fail they are undone and the manifest is left unchanged. A new
// worktree is usable immediately.
func (c *Core) AddMember(p *Package, opts AddOptions) (manifest.Member, error) {
	mem := manifest.Member{Source: opts.Source, Name: opts.Name, Role: strings.Join(strings.Fields(opts.Role), " "), Copy: opts.Copy}
	if opts.Reference {
		mem.Mode, mem.Ref = manifest.ModeReference, opts.Ref
	} else {
		mem.Mode, mem.Base = manifest.ModeActive, opts.Base
	}
	src, err := prepareMember(&mem)
	if err != nil {
		return mem, err
	}

	next := clone(p.M)
	next.Members = append(next.Members, mem)
	next.ApplyDefaults(c.Cfg.BranchPrefix)
	if err := next.Validate(); err != nil {
		return mem, err
	}
	added := next.Members[len(next.Members)-1]

	srcs := map[string]string{}
	for _, existing := range p.M.Members {
		abs, err := paths.Abs(existing.Source)
		if err != nil {
			return added, err
		}
		srcs[existing.Name] = abs
	}
	srcs[added.Name] = src
	existing, err := preflightMembers(next, srcs, []manifest.Member{added}, opts.ReuseBranch)
	if err != nil {
		return added, err
	}

	steps := c.addMemberSteps(&Package{Name: p.Name, Dir: p.Dir, M: next}, added, src, existing[added.Name])
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps); err != nil {
		return added, err
	}
	p.M = next
	if err := c.save(p, "add "+added.Name); err != nil {
		return added, err
	}
	return added, nil
}

// RemoveOptions tunes RemoveMember.
type RemoveOptions struct {
	// Force removes the member even if it has unsaved work.
	Force bool
	// DeleteBranch deletes the package branch if it is merged into its base.
	DeleteBranch bool
}

// RemoveMember removes a member's worktree and drops it from the manifest.
// It returns a *RefusedError, changing nothing, when the member has dirty
// files, untracked files or unpushed commits and force was not given.
func (c *Core) RemoveMember(p *Package, name string, opts RemoveOptions) (DeleteResult, error) {
	var res DeleteResult
	mem, ok := p.M.Member(name)
	if !ok {
		return res, NotFound("package %s has no member %q", p.Name, name)
	}
	target := *mem
	if bad := unsafeOf(c.Safety(p, []manifest.Member{target})); len(bad) > 0 && !opts.Force {
		return res, &RefusedError{Unsafe: bad}
	}
	steps, err := c.removeMemberSteps(p, target, opts.Force, opts.DeleteBranch, &res)
	if err != nil {
		return res, err
	}
	if err := (&apply.Runner{Events: c.Events}).Run(steps); err != nil {
		return res, err
	}
	kept := p.M.Members[:0:0]
	for _, m := range p.M.Members {
		if m.Name != name {
			kept = append(kept, m)
		}
	}
	p.M.Members = kept
	return res, c.save(p, "rm "+name)
}

// ShowMember is one member in Show: manifest fields plus live status.
type ShowMember struct {
	MemberStatus
	Source string   `json:"source"`
	Remote string   `json:"remote,omitempty"`
	Base   string   `json:"base,omitempty"`
	Copy   []string `json:"copy,omitempty"`
	Hooks  bool     `json:"hooks,omitempty"`
}

// ShowNote is a note as reported by Show.
type ShowNote struct {
	At   string `json:"at"`
	By   string `json:"by"`
	Text string `json:"text"`
}

// ShowResult is everything an agent needs to know about its package.
type ShowResult struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Branch  string       `json:"branch"`
	Harness string       `json:"harness,omitempty"`
	Purpose string       `json:"purpose"`
	Context string       `json:"context"`
	Notes   []ShowNote   `json:"notes"`
	Members []ShowMember `json:"members"`
}

// Show reports the purpose, context, notes (newest first) and members with
// mode, role, branch and live git status.
func (c *Core) Show(p *Package) ShowResult {
	out := ShowResult{
		Name: p.Name, Path: p.Dir, Branch: p.M.Branch, Harness: p.M.Harness,
		Purpose: strings.TrimSpace(p.M.Purpose), Context: strings.TrimSpace(p.M.Context),
		Notes: []ShowNote{}, Members: []ShowMember{},
	}
	for _, n := range p.M.RecentNotes(0) {
		out.Notes = append(out.Notes, ShowNote{At: n.At.Format(time.RFC3339), By: n.By, Text: n.Text})
	}
	for _, mem := range p.M.Members {
		base := mem.Base
		if mem.Mode == manifest.ModeReference {
			base = ""
		}
		out.Members = append(out.Members, ShowMember{
			MemberStatus: memberStatus(p, mem),
			Source:       mem.Source, Remote: mem.Remote, Base: base, Copy: mem.Copy, Hooks: mem.Hooks,
		})
	}
	return out
}
