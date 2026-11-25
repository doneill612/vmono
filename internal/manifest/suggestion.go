package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Kinds of Suggestion.
const (
	SuggestSetup    = "setup"
	SuggestTeardown = "teardown"
)

// Bounds on what an agent may suggest, so that a review stays readable.
const (
	MaxSuggestions       = 20
	maxSuggestedCommand  = 500
	maxSuggestionsReason = 300
)

// Suggestion is a setup or teardown command an agent proposed for a member.
// It is inert: nothing runs it until a person accepts it, which adds it to the
// member's own commands.
type Suggestion struct {
	Member  string `toml:"member" json:"member"`
	Kind    string `toml:"kind" json:"kind"`
	Command string `toml:"command" json:"command"`
	// Reason says what in the member the command was derived from.
	Reason string `toml:"reason,omitempty" json:"reason,omitempty"`
}

// ID identifies a suggestion by its content, so that a decision made about one
// command can never apply to another that took its place.
func (s Suggestion) ID() string {
	sum := sha256.Sum256([]byte(s.Member + "\x00" + s.Kind + "\x00" + s.Command))
	return hex.EncodeToString(sum[:4])
}

// Validate checks a suggestion's form. The text is shown to a person who
// decides whether to run it, so it must be one line of characters that show as
// they are: no control characters, and none that reorder or hide others.
func (s Suggestion) Validate() error {
	if s.Kind != SuggestSetup && s.Kind != SuggestTeardown {
		return fmt.Errorf("suggestion for %s: kind must be %q or %q, not %q", s.Member, SuggestSetup, SuggestTeardown, s.Kind)
	}
	if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("suggestion for %s: command is empty", s.Member)
	}
	if len(s.Command) > maxSuggestedCommand || len(s.Reason) > maxSuggestionsReason {
		return fmt.Errorf("suggestion for %s is too long: at most %d bytes of command and %d of reason", s.Member, maxSuggestedCommand, maxSuggestionsReason)
	}
	for _, text := range []string{s.Command, s.Reason} {
		if strings.ContainsFunc(text, func(r rune) bool { return !unicode.IsPrint(r) }) {
			return fmt.Errorf("suggestion for %s: the command and reason must each be one line of printable characters", s.Member)
		}
	}
	return nil
}

// Commands returns the member's commands a suggestion of the given kind would
// join.
func (m *Member) Commands(kind string) *Commands {
	if kind == SuggestTeardown {
		return &m.Teardown
	}
	return &m.Setup
}

// Pending returns the suggestions that still need a decision: those whose
// member exists and does not have the command already, and that were not
// settled before.
func (m *Manifest) Pending() []Suggestion {
	if m.Init == nil {
		return nil
	}
	var out []Suggestion
	seen := map[string]bool{}
	for _, s := range m.Init.Settled {
		seen[s.ID()] = true
	}
	for _, s := range m.Init.Suggestions {
		mem, ok := m.Member(s.Member)
		if !ok || seen[s.ID()] || slices.Contains(*mem.Commands(s.Kind), s.Command) {
			continue
		}
		seen[s.ID()] = true
		out = append(out, s)
	}
	return out
}
