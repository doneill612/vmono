package manifest

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SessionSchemaVersion is the session file schema new sessions are written in.
// Files of schema LandingSessionSchema are still read.
const SessionSchemaVersion = 2

// LandingSessionSchema is the schema of session files from when a session's
// work landed on branches of their own, kept in Into, rather than being pushed
// from the branches it is done on.
const LandingSessionSchema = 1

// SessionFile is the name of a session's metadata file, kept in the same
// .vmono directory a package manifest uses.
const SessionFile = "session.toml"

// Session is one unit of work on a package. A package defines what its
// members are and why; a session holds the worktrees, branch and harness
// directory for one piece of work on them. Every session has every member of
// its package. A session carries no free text of its own: purpose, context,
// notes and roles all belong to the package.
type Session struct {
	Schema  int       `toml:"schema"`
	Package string    `toml:"package"`
	Name    string    `toml:"name"`
	Created time.Time `toml:"created"`
	// Branch is checked out by every active member of the session that Members
	// gives no branch of its own. Use BranchFor to read a member's.
	Branch string `toml:"branch"`
	// Finished and Into are read from files of LandingSessionSchema, and kept
	// until the file is converted: when the session's work last landed, and the
	// branch of each member's source repo it landed on.
	Finished time.Time         `toml:"finished,omitempty"`
	Into     map[string]string `toml:"into,omitempty"`
	// Members maps a member's name to what the session records about it. An
	// active member with no entry is on Branch, which it found already made, and
	// is built on its base. A reference member with none is at the package's ref.
	Members map[string]SessionMember `toml:"members,omitempty"`
}

// SessionMember is what a session records about one member: an active one's
// branch, or what a reference one is checked out at.
type SessionMember struct {
	// Branch is the branch the member has checked out, when it is not the
	// session's.
	Branch string `toml:"branch,omitempty"`
	// Created is true when vmono made the branch, so that one nothing was
	// committed to can be deleted along with the session.
	Created bool `toml:"created,omitempty"`
	// From is the branch or ref the member's branch is built on in place of
	// the member's base, for work stacked on work that has not merged.
	From string `toml:"from,omitempty"`
	// Ref is the branch or ref a reference member is checked out at in place of
	// the package's, for work that depends on work that has not merged.
	Ref string `toml:"ref,omitempty"`
	// BaseTip is the commit of From the branch was last built on, or of Ref that
	// was last checked out. When From goes away it says where the member's own
	// commits begin.
	BaseTip string `toml:"base_tip,omitempty"`
}

// BranchFor returns the branch the named active member has checked out in the
// session.
func (s *Session) BranchFor(member string) string {
	if b := s.Members[member].Branch; b != "" {
		return b
	}
	return s.Branch
}

// RefFor returns the ref the reference member is checked out at in the session.
func (s *Session) RefFor(mem Member) string {
	if r := s.Members[mem.Name].Ref; r != "" {
		return r
	}
	return mem.Ref
}

// SetMember records m for the named member. A branch that is the session's own
// is left out, and an empty record removes the entry.
func (s *Session) SetMember(member string, m SessionMember) {
	if m.Branch == s.Branch {
		m.Branch = ""
	}
	if m == (SessionMember{}) {
		delete(s.Members, member)
		return
	}
	if s.Members == nil {
		s.Members = map[string]SessionMember{}
	}
	s.Members[member] = m
}

// BaseBranch returns the branch a member's base names: "origin/main" is
// "main", "origin/release/1.0" is "release/1.0", and a bare "main" is "main".
// It returns "" when the base names no branch ("origin/HEAD", "HEAD", unset).
func BaseBranch(mem Member) string {
	base := strings.TrimSpace(mem.Base)
	if base == "" || base == "HEAD" || base == "origin/HEAD" {
		return ""
	}
	if _, rest, ok := strings.Cut(base, "/"); ok {
		if rest == "HEAD" {
			return ""
		}
		return rest
	}
	return base
}

// DefaultBranch returns the branch a session is cut on when none is given: its
// name, after the prefix and a slash when there is a prefix.
func DefaultBranch(prefix, session string) string {
	if prefix = strings.Trim(prefix, "/"); prefix != "" {
		return prefix + "/" + session
	}
	return session
}

// ApplyDefaults fills defaulted fields.
func (s *Session) ApplyDefaults() {
	if s.Schema == 0 {
		s.Schema = SessionSchemaVersion
	}
	if s.Branch != "" || s.Name == "" {
		return
	}
	s.Branch = DefaultBranch("", s.Name)
	if s.Schema == LandingSessionSchema {
		s.Branch = "vmono/" + s.Package + "/" + s.Name
	}
}

// Validate checks the schema, that package and session names are slugs, and
// that the branch is set.
func (s *Session) Validate() error {
	if s.Schema != SessionSchemaVersion && s.Schema != LandingSessionSchema {
		return fmt.Errorf("unsupported session schema %d (this vmono reads %d and %d)", s.Schema, LandingSessionSchema, SessionSchemaVersion)
	}
	if err := ValidateName(s.Package); err != nil {
		return fmt.Errorf("session package: %w", err)
	}
	if err := ValidateSessionName(s.Name); err != nil {
		return fmt.Errorf("session name: %w", err)
	}
	if strings.TrimSpace(s.Branch) == "" {
		return fmt.Errorf("session %s: branch is empty", s.Name)
	}
	for member, into := range s.Into {
		if err := ValidateName(member); err != nil {
			return fmt.Errorf("session %s: into: member %q: %w", s.Name, member, err)
		}
		if into == "" {
			return fmt.Errorf("session %s: into: %s has an empty branch", s.Name, member)
		}
		if err := validateBranch(into); err != nil {
			return fmt.Errorf("session %s: into %s: %w", s.Name, member, err)
		}
		if into == s.Branch {
			return fmt.Errorf("session %s: into %s: %q is the session's own branch", s.Name, member, into)
		}
	}
	for member, m := range s.Members {
		if err := ValidateName(member); err != nil {
			return fmt.Errorf("session %s: members: %q: %w", s.Name, member, err)
		}
		if err := validateBranch(m.Branch); err != nil {
			return fmt.Errorf("session %s: branch of %s: %w", s.Name, member, err)
		}
		for field, ref := range map[string]string{"from": m.From, "ref": m.Ref} {
			if strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n") {
				return fmt.Errorf("session %s: %s of %s: %q is not a ref", s.Name, field, member, ref)
			}
		}
	}
	return nil
}

// validateBranch rejects the branch names that can never be valid git branches
// or would be read as options. Full ref-format validation is git's job and
// happens when a session is created. An empty name is one that is not set.
func validateBranch(into string) error {
	switch {
	case into == "":
		return nil
	case strings.TrimSpace(into) != into || strings.ContainsAny(into, " \t\r\n~^:?*[\\"):
		return fmt.Errorf("%q is not a valid branch name", into)
	case strings.HasPrefix(into, "-") || strings.HasPrefix(into, "/") || strings.HasSuffix(into, "/") || strings.HasSuffix(into, ".lock") || strings.Contains(into, ".."):
		return fmt.Errorf("%q is not a valid branch name", into)
	}
	return nil
}

// SessionPath returns the session file path inside a session directory.
func SessionPath(sessionDir string) string { return filepath.Join(sessionDir, Dir, SessionFile) }

// EncodeSession renders a session as TOML with a stable key order.
func EncodeSession(s *Session) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema  = %d\n", s.Schema)
	fmt.Fprintf(&b, "package = %s\n", quote(s.Package))
	fmt.Fprintf(&b, "name    = %s\n", quote(s.Name))
	fmt.Fprintf(&b, "created = %s\n", s.Created.Format(time.RFC3339))
	fmt.Fprintf(&b, "branch  = %s\n", quote(s.Branch))
	if !s.Finished.IsZero() {
		fmt.Fprintf(&b, "finished = %s\n", s.Finished.Format(time.RFC3339))
	}
	if len(s.Into) > 0 {
		members := make([]string, 0, len(s.Into))
		for member := range s.Into {
			members = append(members, member)
		}
		sort.Strings(members)
		b.WriteString("\n[into]\n")
		for _, member := range members {
			fmt.Fprintf(&b, "%s = %s\n", member, quote(s.Into[member]))
		}
	}
	for _, member := range slices.Sorted(maps.Keys(s.Members)) {
		m := s.Members[member]
		fmt.Fprintf(&b, "\n[members.%s]\n", member)
		if m.Branch != "" {
			fmt.Fprintf(&b, "branch = %s\n", quote(m.Branch))
		}
		if m.Created {
			b.WriteString("created = true\n")
		}
		if m.From != "" {
			fmt.Fprintf(&b, "from = %s\n", quote(m.From))
		}
		if m.Ref != "" {
			fmt.Fprintf(&b, "ref = %s\n", quote(m.Ref))
		}
		if m.BaseTip != "" {
			fmt.Fprintf(&b, "base_tip = %s\n", quote(m.BaseTip))
		}
	}
	return []byte(b.String())
}

// ParseSession decodes session bytes, rejecting unknown keys so typos
// surface.
func ParseSession(data []byte) (*Session, error) {
	var s Session
	md, err := toml.Decode(string(data), &s)
	if err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("parse session: unknown keys: %s", strings.Join(keys, ", "))
	}
	s.ApplyDefaults()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// LoadSession reads and validates the session file in sessionDir.
func LoadSession(sessionDir string) (*Session, error) {
	data, err := os.ReadFile(SessionPath(sessionDir))
	if err != nil {
		return nil, err
	}
	return ParseSession(data)
}

// SaveSession validates and writes the session file atomically to
// sessionDir.
func SaveSession(sessionDir string, s *Session) error {
	if err := s.Validate(); err != nil {
		return err
	}
	file := SessionPath(sessionDir)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, EncodeSession(s), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// FindSession locates the session directory for the current context. It
// walks up from start looking for .vmono/session.toml, then falls back to
// VMONO_ROOT, which vmono sets to the session directory inside a harness. It
// returns os.ErrNotExist when neither applies, including when start is in a
// package directory outside any session.
func FindSession(start string) (string, error) {
	if dir, ok := walkUp(start, SessionPath); ok {
		return dir, nil
	}
	if root := os.Getenv("VMONO_ROOT"); root != "" {
		if dir, ok := walkUp(root, SessionPath); ok {
			return dir, nil
		}
	}
	return "", os.ErrNotExist
}
