package render

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/doneill612/vmono/internal/manifest"
)

// Adapter names, matching the `adapter` key of a harness definition.
const (
	AdapterClaudeCode = "claude-code"
	AdapterOpenCode   = "opencode"
)

// Options carries what rendering needs beyond the manifest.
type Options struct {
	// Adapters is the set of adapter names to generate files for. Empty
	// means none.
	Adapters []string
	// OpenCodeV2 selects OpenCode's V2 permissions array instead of the
	// 1.x permission object.
	OpenCodeV2 bool
}

// has reports whether an adapter is enabled.
func (o Options) has(name string) bool {
	for _, a := range o.Adapters {
		if a == name {
			return true
		}
	}
	return false
}

// writer records which generated files actually changed.
type writer struct {
	dir     string
	changed []string
}

// abs returns the absolute path of a slash-separated relative path.
func (w *writer) abs(rel string) string {
	return filepath.Join(w.dir, filepath.FromSlash(rel))
}

// file writes content to rel only when it differs from what is on disk, so
// a quick sync at launch is cheap and leaves mtimes alone.
func (w *writer) file(rel, content string) error {
	return w.fileMode(rel, content, 0o644)
}

// fileMode is file with explicit permission bits, used for skill scripts
// that must stay executable.
func (w *writer) fileMode(rel, content string, perm fs.FileMode) error {
	return w.fileWith(rel, perm, func(string) (string, error) { return content, nil })
}

// fileWith is file for content derived from the existing file, such as the
// AGENTS.md marker merge.
func (w *writer) fileWith(rel string, perm fs.FileMode, build func(existing string) (string, error)) error {
	abs := w.abs(rel)
	var existing []byte
	if data, err := os.ReadFile(abs); err == nil {
		existing = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out, err := build(string(existing))
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if existing != nil && bytes.Equal(existing, []byte(out)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, []byte(out), perm); err != nil {
		return err
	}
	w.changed = append(w.changed, rel)
	return nil
}

// remove deletes a generated file or tree if present.
func (w *writer) remove(rel string) error {
	abs := w.abs(rel)
	if _, err := os.Lstat(abs); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return err
	}
	w.changed = append(w.changed, rel)
	return nil
}

// RenderPackage writes the package directory's own generated file, its
// .gitignore, and returns the relative paths that changed. Harnesses open a
// session, never the package directory, so nothing else is generated here.
// It never touches git.
func RenderPackage(pkgDir string) ([]string, error) {
	w := &writer{dir: pkgDir}
	if err := w.file(".gitignore", PackageGitignore()); err != nil {
		return w.changed, err
	}
	return w.changed, nil
}

// Render writes every generated file for a session into sessionDir and
// returns the relative paths whose content changed. The package manifest m
// supplies the text and members; the session supplies the branch. It never
// touches git.
func Render(sessionDir string, m *manifest.Manifest, s *manifest.Session, opts Options) ([]string, error) {
	w := &writer{dir: sessionDir}

	block := AgentsBlock(m, s)
	if err := w.fileWith("AGENTS.md", 0o644, func(existing string) (string, error) {
		return MergeAgents(existing, block)
	}); err != nil {
		return w.changed, err
	}
	if err := w.file("CLAUDE.md", ClaudeMD); err != nil {
		return w.changed, err
	}
	if err := w.file(".gitignore", Gitignore(m)); err != nil {
		return w.changed, err
	}
	if opts.has(AdapterClaudeCode) {
		if err := renderClaudeCode(w, m); err != nil {
			return w.changed, fmt.Errorf("claude-code adapter: %w", err)
		}
	}
	if opts.has(AdapterOpenCode) {
		if err := renderOpenCode(w, m, opts.OpenCodeV2); err != nil {
			return w.changed, fmt.Errorf("opencode adapter: %w", err)
		}
	}
	return w.changed, nil
}
