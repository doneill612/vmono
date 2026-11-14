package render

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/doneill612/vmono/internal/manifest"
)

// Options carries what rendering needs beyond the manifest.
type Options struct {
	// Adapters is the set of adapter names ("claude-code", "opencode") to
	// generate files for. Empty means none.
	Adapters []string
}

// Render writes every generated file for the package into pkgDir and
// returns the relative paths whose content changed. Files are rewritten
// only when their content differs, so a quick sync at launch is cheap and
// leaves mtimes alone. Render never touches git.
func Render(pkgDir string, m *manifest.Manifest, opts Options) ([]string, error) {
	var changed []string
	write := func(rel, content string, merge func(existing string) (string, error)) error {
		abs := filepath.Join(pkgDir, filepath.FromSlash(rel))
		var existing []byte
		if data, err := os.ReadFile(abs); err == nil {
			existing = data
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		out := content
		if merge != nil {
			merged, err := merge(string(existing))
			if err != nil {
				return fmt.Errorf("%s: %w", rel, err)
			}
			out = merged
		}
		if bytes.Equal(existing, []byte(out)) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
			return err
		}
		changed = append(changed, rel)
		return nil
	}

	block := AgentsBlock(m)
	if err := write("AGENTS.md", block, func(existing string) (string, error) {
		return MergeAgents(existing, block)
	}); err != nil {
		return changed, err
	}
	if err := write("CLAUDE.md", ClaudeMD, nil); err != nil {
		return changed, err
	}
	if err := write(".gitignore", Gitignore(m), nil); err != nil {
		return changed, err
	}
	return changed, nil
}
