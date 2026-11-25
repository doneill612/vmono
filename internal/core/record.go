package core

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/manifest"
)

// InitRecord is what an agent submits after exploring a package.
type InitRecord struct {
	// Purpose is optional, and so is each role. Overview is required.
	Purpose  string            `json:"purpose"`
	Overview string            `json:"overview"`
	Roles    map[string]string `json:"roles"`
}

// RecordResult reports which parts of an InitRecord were written. Parts are
// named "overview", "purpose" and "role:<member>".
type RecordResult struct {
	Set []string `json:"set"`
	// Kept are parts the user wrote or edited, which were left as they are.
	Kept []string `json:"kept"`
}

// RecordInit writes what an agent learned about a package, as one change.
//
// The overview replaces the previous one. The purpose and each role are
// written only when empty or still exactly what an earlier record wrote, so
// the agent can revise its own text while the user's is always kept. The
// context and notes are never touched.
func (c *Core) RecordInit(p *Package, rec InitRecord) (RecordResult, error) {
	res := RecordResult{Set: []string{}, Kept: []string{}}
	overview, purpose := strings.TrimSpace(rec.Overview), strings.TrimSpace(rec.Purpose)
	if overview == "" {
		return res, errors.New("overview is empty")
	}
	for _, name := range slices.Sorted(maps.Keys(rec.Roles)) {
		if _, ok := p.M.Member(name); !ok {
			return res, NotFound("package %s has no member %q", p.Name, name)
		}
	}

	next := clone(p.M)
	old := next.Init
	if old == nil {
		old = &manifest.Init{}
	}
	in := &manifest.Init{
		At: time.Now().Truncate(time.Second), By: c.Author,
		Overview: overview, Purpose: old.Purpose, Roles: map[string]string{},
		Members: c.exploredMembers(p),
	}
	maps.Copy(in.Roles, old.Roles)
	next.Init = in
	res.Set = append(res.Set, "overview")

	// fill writes text over *live when that is allowed and returns what init
	// is now known to have written there.
	fill := func(part, text string, live *string, wrote string) string {
		current := strings.TrimSpace(*live)
		switch {
		case text == "":
			return wrote
		case current != "" && current != strings.TrimSpace(wrote):
			res.Kept = append(res.Kept, part)
			return wrote
		}
		*live = text
		res.Set = append(res.Set, part)
		return text
	}
	in.Purpose = fill("purpose", purpose, &next.Purpose, in.Purpose)
	for _, name := range slices.Sorted(maps.Keys(rec.Roles)) {
		mem, _ := next.Member(name)
		role := strings.Join(strings.Fields(rec.Roles[name]), " ")
		if wrote := fill("role:"+name, role, &mem.Role, in.Roles[name]); wrote != "" {
			in.Roles[name] = wrote
		}
	}
	p.M = next
	return res, c.save(p, "package init")
}

// memberNames returns the names of a package's members in manifest order.
func memberNames(m *manifest.Manifest) []string {
	out := make([]string, 0, len(m.Members))
	for _, mem := range m.Members {
		out = append(out, mem.Name)
	}
	return out
}

// exploredMembers returns the members a record covers.
func (c *Core) exploredMembers(p *Package) []string { return memberNames(p.M) }
