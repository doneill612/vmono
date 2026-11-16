package manifest

import (
	"reflect"
	"strings"
)

// Plan is the set of member-level changes needed to turn one manifest into
// another. Package-level fields (purpose, context, harness) only need files
// regenerated and are reported via MetaChanged.
type Plan struct {
	// Add are members present only in the new manifest.
	Add []Member
	// Remove are members present only in the old manifest.
	Remove []Member
	// Switch are members whose mode changed. Each is a remove of Old
	// followed by an add of New; the branch is kept.
	Switch []Switch
	// Update are members whose worktree is unaffected but whose metadata
	// (role, copy, hooks) changed.
	Update []Member
	// MetaChanged is true when purpose, context or harness changed.
	MetaChanged bool
}

// Switch is a mode change for one member.
type Switch struct {
	Old Member
	New Member
}

// Empty reports whether the plan changes nothing.
func (p Plan) Empty() bool {
	return len(p.Add) == 0 && len(p.Remove) == 0 && len(p.Switch) == 0 &&
		len(p.Update) == 0 && !p.MetaChanged
}

// Removals returns every member whose worktree will be removed, including
// the old side of mode switches. These are the removals that need the same
// safety confirmation as delete.
func (p Plan) Removals() []Member {
	out := append([]Member(nil), p.Remove...)
	for _, s := range p.Switch {
		out = append(out, s.Old)
	}
	return out
}

// Diff compares old and new manifests by member name.
//
// A change to a member's source, path, base or ref is treated as removing
// and re-adding it, since the existing worktree no longer matches. Purpose
// and context are compared ignoring trailing whitespace because the
// multi-line encoding normalizes a trailing newline.
func Diff(oldM, newM *Manifest) Plan {
	var p Plan
	oldBy := map[string]Member{}
	for _, m := range oldM.Members {
		oldBy[m.Name] = m
	}
	newBy := map[string]Member{}
	for _, m := range newM.Members {
		newBy[m.Name] = m
	}
	for _, nm := range newM.Members {
		om, ok := oldBy[nm.Name]
		if !ok {
			p.Add = append(p.Add, nm)
			continue
		}
		switch {
		case om.Mode != nm.Mode:
			p.Switch = append(p.Switch, Switch{Old: om, New: nm})
		case om.Source != nm.Source || om.Path != nm.Path || om.Base != nm.Base || om.Ref != nm.Ref:
			p.Remove = append(p.Remove, om)
			p.Add = append(p.Add, nm)
		case om.Role != nm.Role || om.Hooks != nm.Hooks || om.Remote != nm.Remote || !reflect.DeepEqual(nilIfEmpty(om.Copy), nilIfEmpty(nm.Copy)):
			p.Update = append(p.Update, nm)
		}
	}
	for _, om := range oldM.Members {
		if _, ok := newBy[om.Name]; !ok {
			p.Remove = append(p.Remove, om)
		}
	}
	p.MetaChanged = strings.TrimRight(oldM.Purpose, " \t\r\n") != strings.TrimRight(newM.Purpose, " \t\r\n") ||
		strings.TrimRight(oldM.Context, " \t\r\n") != strings.TrimRight(newM.Context, " \t\r\n") ||
		oldM.Harness != newM.Harness
	return p
}

// nilIfEmpty normalizes empty slices so DeepEqual treats nil and [] alike.
func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}
