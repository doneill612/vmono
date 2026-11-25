package manifest

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Init is what `vmono package init` recorded: what an agent learned by
// exploring the members. It is kept apart from the text a person writes, so
// initializing again replaces the agent's text and never theirs.
type Init struct {
	// At is when it was recorded, and By the author that did.
	At time.Time `toml:"at"`
	By string    `toml:"by"`
	// Members names the members that were explored, so a later change to the
	// member list can be reported (see Manifest.InitDrift).
	Members []string `toml:"members"`
	// Overview is the agent's account of the package.
	Overview string `toml:"overview"`
	// Purpose and Roles are copies of the purpose and the member roles that
	// init itself wrote. While one still matches the live text, nobody has
	// edited it since and init may replace it; otherwise it is the user's.
	Purpose string            `toml:"purpose,omitempty"`
	Roles   map[string]string `toml:"roles,omitempty"`
	// Suggestions are commands the agent proposed; see Manifest.Pending.
	// Settled are ones a person accepted or dismissed, which are not offered
	// again.
	Suggestions []Suggestion `toml:"suggestion"`
	Settled     []Suggestion `toml:"settled"`
}

// validate checks that a recorded init says something and that what it
// suggests is fit to be reviewed.
func (i *Init) validate() error {
	if strings.TrimSpace(i.Overview) == "" {
		return errors.New("init: overview is empty")
	}
	for _, s := range append(append([]Suggestion(nil), i.Suggestions...), i.Settled...) {
		if err := s.Validate(); err != nil {
			return fmt.Errorf("init: %w", err)
		}
	}
	return nil
}

// Overview returns the recorded overview, or "" when the package has none.
func (m *Manifest) Overview() string {
	if m.Init == nil {
		return ""
	}
	return strings.TrimSpace(m.Init.Overview)
}

// InitDrift compares the members that were explored with the ones the package
// has now, in manifest and recorded order. Both are nil when the package was
// never initialized or nothing changed. A member whose source changed under
// the same name is not noticed.
func (m *Manifest) InitDrift() (added, removed []string) {
	if m.Init == nil {
		return nil, nil
	}
	explored := map[string]bool{}
	for _, name := range m.Init.Members {
		explored[name] = true
		if _, ok := m.Member(name); !ok {
			removed = append(removed, name)
		}
	}
	for _, mem := range m.Members {
		if !explored[mem.Name] {
			added = append(added, mem.Name)
		}
	}
	return added, removed
}

// DriftText renders InitDrift as "+added -removed", or "" when there is none.
func (m *Manifest) DriftText() string {
	added, removed := m.InitDrift()
	var parts []string
	for _, name := range added {
		parts = append(parts, "+"+name)
	}
	for _, name := range removed {
		parts = append(parts, "-"+name)
	}
	return strings.Join(parts, " ")
}
