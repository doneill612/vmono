package core

import (
	"errors"
	"fmt"
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
	// Suggestions are setup and teardown commands for a person to review. A
	// list replaces the suggestions made before; nil leaves them.
	Suggestions []manifest.Suggestion `json:"suggestions"`
}

// RecordResult reports which parts of an InitRecord were written. Parts are
// named "overview", "purpose" and "role:<member>".
type RecordResult struct {
	Set []string `json:"set"`
	// Kept are parts the user wrote or edited, which were left as they are.
	Kept []string `json:"kept"`
	// Pending are the suggestions that await review, and NewPending how many
	// of them this record added.
	Pending    []manifest.Suggestion `json:"pending"`
	NewPending int                   `json:"new_pending"`
}

// ErrAgentReview is returned when an agent decides on suggested commands.
var ErrAgentReview = errors.New("an agent cannot accept or dismiss suggested commands; the user reviews them when they exit, or with `vmono package suggestions`")

// RecordInit writes what an agent learned about a package, as one change.
//
// The overview replaces the previous one. The purpose and each role are
// written only when empty or still exactly what an earlier record wrote, so
// the agent can revise its own text while the user's is always kept. The
// context and notes are never touched. Suggested commands are only stored:
// see ReviewSuggestions.
func (c *Core) RecordInit(p *Package, rec InitRecord) (RecordResult, error) {
	res := RecordResult{Set: []string{}, Kept: []string{}, Pending: []manifest.Suggestion{}}
	overview, purpose := strings.TrimSpace(rec.Overview), strings.TrimSpace(rec.Purpose)
	if overview == "" {
		return res, errors.New("overview is empty")
	}
	for _, name := range slices.Sorted(maps.Keys(rec.Roles)) {
		if _, ok := p.M.Member(name); !ok {
			return res, NotFound("package %s has no member %q", p.Name, name)
		}
	}

	if len(rec.Suggestions) > manifest.MaxSuggestions {
		return res, fmt.Errorf("too many suggestions: %d, at most %d", len(rec.Suggestions), manifest.MaxSuggestions)
	}
	for i := range rec.Suggestions {
		s := &rec.Suggestions[i]
		s.Command, s.Reason = strings.TrimSpace(s.Command), strings.TrimSpace(s.Reason)
		if _, ok := p.M.Member(s.Member); !ok {
			return res, NotFound("package %s has no member %q", p.Name, s.Member)
		}
		if err := s.Validate(); err != nil {
			return res, err
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
		Members: c.exploredMembers(p), Suggestions: old.Suggestions, Settled: old.Settled,
	}
	if rec.Suggestions != nil {
		in.Suggestions = rec.Suggestions
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
	before := map[string]bool{}
	for _, s := range p.M.Pending() {
		before[s.ID()] = true
	}
	in.Suggestions = next.Pending() // settled ones need not be kept
	for _, s := range in.Suggestions {
		res.Pending = append(res.Pending, s)
		if !before[s.ID()] {
			res.NewPending++
		}
	}
	p.M = next
	return res, c.save(p, "package init")
}

// SuggestionDecision settles one suggestion.
type SuggestionDecision struct {
	// ID is the suggestion's, which is bound to its exact command.
	ID string
	// Accept adds the command to the member; otherwise it is dismissed.
	Accept bool
	// Command replaces the suggested command when not empty.
	Command string
}

// ReviewSuggestions settles suggestions, as one change. Accepted commands are
// added after the member's own. They run in worktrees created or removed from
// then on, and nothing runs now. An agent is refused, since the commands run
// as the user and outside any sandbox. An unknown id settles nothing.
func (c *Core) ReviewSuggestions(p *Package, decisions []SuggestionDecision) error {
	if strings.HasPrefix(c.Author, "agent:") {
		return ErrAgentReview
	}
	next := clone(p.M)
	for _, d := range decisions {
		pending := next.Pending()
		at := slices.IndexFunc(pending, func(s manifest.Suggestion) bool { return s.ID() == d.ID })
		if at < 0 {
			return NotFound("package %s has no pending suggestion %q", p.Name, d.ID)
		}
		s := pending[at]
		command := strings.TrimSpace(d.Command)
		if d.Accept {
			if command == "" {
				command = s.Command
			}
			mem, _ := next.Member(s.Member)
			if cmds := mem.Commands(s.Kind); !slices.Contains(*cmds, command) {
				*cmds = append(*cmds, command)
			}
		}
		// Also when accepted, so that editing or removing the command later
		// does not bring the suggestion back.
		next.Init.Settled = append(next.Init.Settled, s)
		next.Init.Suggestions = next.Pending()
	}
	if len(decisions) == 0 {
		return nil
	}
	if err := next.Validate(); err != nil {
		return err
	}
	p.M = next
	return c.save(p, "review suggestions")
}

// memberNames returns the names of a package's members in manifest order.
func memberNames(m *manifest.Manifest) []string {
	out := make([]string, 0, len(m.Members))
	for _, mem := range m.Members {
		out = append(out, mem.Name)
	}
	return out
}
