package manifest

import (
	"errors"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// ErrLegacy matches any *LegacyError with errors.Is.
var ErrLegacy = errors.New("package uses the pre-session layout")

// LegacyError reports a schema 1 manifest: a package from before sessions
// existed, whose branch and worktrees lived in the package itself. Such a
// package is read only by the migration, which turns it into a package with
// one session.
type LegacyError struct {
	// Branch is the package branch the legacy manifest recorded. The
	// migration keeps it as the migrated session's branch.
	Branch string
}

// Error implements error.
func (e *LegacyError) Error() string {
	return "package predates sessions; run `vmono doctor --fix` to migrate it"
}

// Is makes errors.Is(err, ErrLegacy) true.
func (e *LegacyError) Is(target error) bool { return target == ErrLegacy }

// detectLegacy returns a *LegacyError when data declares schema 1. It reads
// only the two keys it needs, so it works even though the full legacy file
// has a key (branch) the current Manifest rejects.
func detectLegacy(data []byte) *LegacyError {
	var head struct {
		Schema int    `toml:"schema"`
		Branch string `toml:"branch"`
	}
	if _, err := toml.Decode(string(data), &head); err != nil || head.Schema != LegacySchemaVersion {
		return nil
	}
	return &LegacyError{Branch: head.Branch}
}

// legacyManifest is the schema 1 file: today's manifest plus the branch.
type legacyManifest struct {
	Manifest
	Branch string `toml:"branch"`
}

// LoadLegacy reads a schema 1 manifest from pkgDir for migration. It returns
// the manifest converted to the current model (schema 2, no branch) and the
// package branch the file recorded, defaulting to "vmono/<name>" when the
// hand-written file omitted it, as schema 1 allowed.
func LoadLegacy(pkgDir string) (*Manifest, string, error) {
	data, err := os.ReadFile(Path(pkgDir))
	if err != nil {
		return nil, "", err
	}
	if detectLegacy(data) == nil {
		return nil, "", fmt.Errorf("%s is not a schema %d manifest", Path(pkgDir), LegacySchemaVersion)
	}
	var lm legacyManifest
	md, err := toml.Decode(string(data), &lm)
	if err != nil {
		return nil, "", fmt.Errorf("parse legacy manifest: %w", err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return nil, "", fmt.Errorf("parse legacy manifest: unknown keys: %v", u)
	}
	m := lm.Manifest
	m.Schema = SchemaVersion
	m.ApplyDefaults()
	branch := lm.Branch
	if branch == "" {
		branch = "vmono/" + m.Name
	}
	if err := m.Validate(); err != nil {
		return nil, "", err
	}
	return &m, branch, nil
}
