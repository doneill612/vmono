// Package manifest defines a package's source-of-truth file,
// .vmono/manifest.toml: its model, validation, stable TOML encoding, and the
// diff between two manifests that drives edit and sync.
package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the manifest schema this build reads and writes. Schema 1
// was the single-state package that held its own branch and worktrees;
// schema 2 is the session-based package, where the branch and worktrees
// live in sessions. See LegacyError.
const SchemaVersion = 2

// LegacySchemaVersion is the manifest schema that predates sessions.
const LegacySchemaVersion = 1

// Dir and File locate the manifest inside a package directory.
const (
	Dir  = ".vmono"
	File = "manifest.toml"
)

// Member modes.
const (
	ModeActive    = "active"
	ModeReference = "reference"
)

// MaxNotesRendered caps how many notes are rendered into AGENTS.md.
const MaxNotesRendered = 20

var slugRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Member is a source repo included in a package as a git worktree.
type Member struct {
	Name   string   `toml:"name"`
	Source string   `toml:"source"`
	Remote string   `toml:"remote,omitempty"`
	Mode   string   `toml:"mode"`
	Path   string   `toml:"path"`
	Base   string   `toml:"base,omitempty"`
	Ref    string   `toml:"ref,omitempty"`
	Role   string   `toml:"role,omitempty"`
	Copy   []string `toml:"copy,omitempty"`
	Hooks  bool     `toml:"hooks,omitempty"`
}

// Note is one dated entry in the package log.
type Note struct {
	At   time.Time `toml:"at"`
	By   string    `toml:"by"`
	Text string    `toml:"text"`
}

// Manifest describes a package completely: its members and the text that
// explains them. It holds no branch or worktree state; that belongs to a
// Session.
type Manifest struct {
	Schema  int       `toml:"schema"`
	Name    string    `toml:"name"`
	Created time.Time `toml:"created"`
	Harness string    `toml:"harness,omitempty"`
	Purpose string    `toml:"purpose,omitempty"`
	Context string    `toml:"context,omitempty"`
	Members []Member  `toml:"member"`
	Notes   []Note    `toml:"note"`
}

// ValidateName checks a package or member name against the slug rules.
func ValidateName(name string) error {
	if !slugRE.MatchString(name) {
		return fmt.Errorf("invalid name %q: use lowercase letters, digits and single hyphens", name)
	}
	return nil
}

// Slugify turns a directory name into a valid slug, for default member
// names. It returns "" when nothing usable remains.
func Slugify(s string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// DefaultPath returns the default member path for a name and mode.
func DefaultPath(name, mode string) string {
	if mode == ModeReference {
		return "_ref/" + name
	}
	return name
}

// ApplyDefaults fills defaulted fields.
func (m *Manifest) ApplyDefaults() {
	if m.Schema == 0 {
		m.Schema = SchemaVersion
	}
	for i := range m.Members {
		mem := &m.Members[i]
		if mem.Path == "" {
			mem.Path = DefaultPath(mem.Name, mem.Mode)
		}
		switch mem.Mode {
		case ModeActive:
			if mem.Base == "" {
				mem.Base = "origin/HEAD"
			}
		case ModeReference:
			if mem.Ref == "" {
				mem.Ref = "origin/HEAD"
			}
		}
	}
}

// Validate checks every rule in the spec's field table and that member
// paths are relative, unique, inside the package and not nested in .vmono.
func (m *Manifest) Validate() error {
	var errs []error
	if m.Schema != SchemaVersion {
		errs = append(errs, fmt.Errorf("unsupported schema %d (this vmono reads %d)", m.Schema, SchemaVersion))
	}
	if err := ValidateName(m.Name); err != nil {
		errs = append(errs, err)
	}
	names := map[string]bool{}
	paths := map[string]bool{}
	for i, mem := range m.Members {
		where := fmt.Sprintf("member %d (%s)", i+1, mem.Name)
		if err := ValidateName(mem.Name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		}
		if names[mem.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate member name", where))
		}
		names[mem.Name] = true
		if mem.Mode != ModeActive && mem.Mode != ModeReference {
			errs = append(errs, fmt.Errorf("%s: mode must be %q or %q", where, ModeActive, ModeReference))
		}
		if mem.Source == "" {
			errs = append(errs, fmt.Errorf("%s: source is empty", where))
		}
		if err := validateMemberPath(mem.Path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
		} else {
			key := strings.ToLower(filepath.ToSlash(filepath.Clean(mem.Path)))
			if paths[key] {
				errs = append(errs, fmt.Errorf("%s: duplicate path %q", where, mem.Path))
			}
			paths[key] = true
			if mem.Mode == ModeActive && strings.HasPrefix(key, "_ref/") {
				errs = append(errs, fmt.Errorf("%s: active members cannot live under _ref/", where))
			}
			if mem.Mode == ModeReference && !strings.HasPrefix(key, "_ref/") {
				errs = append(errs, fmt.Errorf("%s: reference members must live under _ref/", where))
			}
		}
	}
	return errors.Join(errs...)
}

// validateMemberPath rejects absolute paths, traversal, and reserved
// locations.
func validateMemberPath(p string) error {
	if p == "" {
		return errors.New("path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return fmt.Errorf("path %q must be relative", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path %q escapes the package", p)
	}
	first := strings.Split(clean, "/")[0]
	if first == ".git" || first == ".vmono" || first == ".claude" || first == ".agents" {
		return fmt.Errorf("path %q is reserved", p)
	}
	return nil
}

// Member returns the named member.
func (m *Manifest) Member(name string) (*Member, bool) {
	for i := range m.Members {
		if m.Members[i].Name == name {
			return &m.Members[i], true
		}
	}
	return nil, false
}

// Active returns the active members in manifest order.
func (m *Manifest) Active() []Member { return m.byMode(ModeActive) }

// References returns the reference members in manifest order.
func (m *Manifest) References() []Member { return m.byMode(ModeReference) }

func (m *Manifest) byMode(mode string) []Member {
	var out []Member
	for _, mem := range m.Members {
		if mem.Mode == mode {
			out = append(out, mem)
		}
	}
	return out
}

// RecentNotes returns up to limit notes, newest first.
func (m *Manifest) RecentNotes(limit int) []Note {
	notes := append([]Note(nil), m.Notes...)
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At.After(notes[j].At) })
	if limit > 0 && len(notes) > limit {
		notes = notes[:limit]
	}
	return notes
}

// Path returns the manifest file path inside a package directory.
func Path(pkgDir string) string { return filepath.Join(pkgDir, Dir, File) }

// Load reads, defaults and validates the manifest in pkgDir.
func Load(pkgDir string) (*Manifest, error) {
	return LoadFile(Path(pkgDir))
}

// LoadFile reads, defaults and validates a manifest file.
func LoadFile(file string) (*Manifest, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes manifest bytes, rejecting unknown keys so typos surface. A
// schema 1 manifest is not parsed; it returns a *LegacyError so callers can
// point the user at `vmono doctor --fix`.
func Parse(data []byte) (*Manifest, error) {
	if legacy := detectLegacy(data); legacy != nil {
		return nil, legacy
	}
	var m Manifest
	md, err := toml.Decode(string(data), &m)
	if err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, len(u))
		for i, k := range u {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("parse manifest: unknown keys: %s", strings.Join(keys, ", "))
	}
	m.ApplyDefaults()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Save validates and writes the manifest atomically to pkgDir.
func Save(pkgDir string, m *Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	file := Path(pkgDir)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, Encode(m), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// walkUp returns the nearest directory at or above start for which file(dir)
// exists.
func walkUp(start string, file func(dir string) string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(file(dir)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Find locates the package directory for the current context. It walks up
// from start looking for .vmono/manifest.toml, which also finds the package
// from inside one of its sessions, then falls back to the same walk from
// VMONO_ROOT. VMONO_ROOT is the session directory inside a harness. It
// returns os.ErrNotExist when neither applies.
func Find(start string) (string, error) {
	if dir, ok := walkUp(start, Path); ok {
		return dir, nil
	}
	if root := os.Getenv("VMONO_ROOT"); root != "" {
		if dir, ok := walkUp(root, Path); ok {
			return dir, nil
		}
	}
	return "", os.ErrNotExist
}
