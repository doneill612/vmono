package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SessionSchemaVersion is the session file schema this build reads and writes.
const SessionSchemaVersion = 1

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
	// Branch is checked out by every active member of the session.
	Branch string `toml:"branch"`
	// Into is the branch the session's work lands on in each member's source
	// repo when the session is finished. Empty means each member's own base
	// branch (see IntoFor).
	Into string `toml:"into,omitempty"`
	// Finished is when the session's work last landed. The zero time means it
	// has never been finished. Whether the session still reads as finished
	// also depends on its members' live git state; see core.SessionState.
	Finished time.Time `toml:"finished,omitempty"`
}

// IntoFor returns the branch mem's work lands on when the session is finished,
// and whether the session named it explicitly. An explicit branch that does
// not exist yet is created at finish. Without one, the branch is read from
// the member's base: "origin/main" lands on "main", "origin/release/1.0" on
// "release/1.0", and a bare "main" on "main". It returns "" when the base
// names no branch ("origin/HEAD", "HEAD"); the caller then resolves the
// source repo's default branch with git.
func (s *Session) IntoFor(mem Member) (branch string, explicit bool) {
	if s.Into != "" {
		return s.Into, true
	}
	base := strings.TrimSpace(mem.Base)
	if base == "" || base == "HEAD" || base == "origin/HEAD" {
		return "", false
	}
	if _, rest, ok := strings.Cut(base, "/"); ok {
		if rest == "HEAD" {
			return "", false
		}
		return rest, false
	}
	return base, false
}

// DefaultBranch returns the branch a session of pkg is cut on when none is
// given: the prefix, then the package, then the session name. Including the
// package keeps sessions of different packages from sharing a branch in a
// repo they both use.
func DefaultBranch(prefix, pkg, session string) string {
	return prefix + pkg + "/" + session
}

// ApplyDefaults fills defaulted fields. branchPrefix is used when the session
// has no branch.
func (s *Session) ApplyDefaults(branchPrefix string) {
	if s.Schema == 0 {
		s.Schema = SessionSchemaVersion
	}
	if s.Branch == "" && s.Package != "" && s.Name != "" {
		s.Branch = DefaultBranch(branchPrefix, s.Package, s.Name)
	}
}

// Validate checks the schema, that package and session names are slugs, and
// that the branch is set.
func (s *Session) Validate() error {
	if s.Schema != SessionSchemaVersion {
		return fmt.Errorf("unsupported session schema %d (this vmono reads %d)", s.Schema, SessionSchemaVersion)
	}
	if err := ValidateName(s.Package); err != nil {
		return fmt.Errorf("session package: %w", err)
	}
	if err := ValidateName(s.Name); err != nil {
		return fmt.Errorf("session name: %w", err)
	}
	if strings.TrimSpace(s.Branch) == "" {
		return fmt.Errorf("session %s: branch is empty", s.Name)
	}
	if err := validateInto(s.Into); err != nil {
		return fmt.Errorf("session %s: into: %w", s.Name, err)
	}
	if s.Into != "" && s.Into == s.Branch {
		return fmt.Errorf("session %s: into %q is the session's own branch", s.Name, s.Into)
	}
	return nil
}

// validateInto rejects the landing branch names that can never be valid git
// branches or would be read as options. Full ref-format validation is git's
// job and happens when a session is created.
func validateInto(into string) error {
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
	if s.Into != "" {
		fmt.Fprintf(&b, "into    = %s\n", quote(s.Into))
	}
	if !s.Finished.IsZero() {
		fmt.Fprintf(&b, "finished = %s\n", s.Finished.Format(time.RFC3339))
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
	s.ApplyDefaults("vmono/")
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
