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
	// AdapterAgentSkills is for harnesses, such as Codex and Pi, that load
	// skills from a project's .agents/skills/ but need no other generated
	// config. It writes the bundled skill there and nothing else.
	AdapterAgentSkills = "agent-skills"
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

// agentSkillsDir is where the agent-skills adapter puts the bundled skill:
// the project-level skills directory that Codex and Pi both scan, from the
// working directory up to the repository root. A session is its own git
// repository, so its root is that root.
const agentSkillsDir = ".agents/skills"

// pluginWriters maps an adapter to the function that writes its harness's
// full plugin. An adapter missing from it has no plugin strategy and gets
// the bundled skill alone. Adding a plugin for another harness is adding an
// entry here.
var pluginWriters = map[string]func(*writer) error{
	AdapterClaudeCode: writeClaudePlugin,
}

// writeIntegration puts vmono's guidance where the adapter's harness will
// find it: the adapter's plugin when pluginWriters has one, and otherwise the
// bundled skill under skillsDir. Both put the same SKILL.md at
// skillsDir/vmono, so a harness can always read the skill whichever path was
// taken.
func writeIntegration(w *writer, adapter, skillsDir string) error {
	if write, ok := pluginWriters[adapter]; ok {
		return write(w)
	}
	return writeBundledSkill(w, skillsDir)
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

// tree makes the directory root hold exactly files, a map of paths relative
// to root to their content. Each file is written only when it differs, and
// every file or link already under root that is not in files is removed,
// along with the directories that leaves empty. It is for directories vmono
// owns outright; it must never be pointed at one that holds the user's own
// files.
func (w *writer) tree(root string, files map[string]string) error {
	keep := make(map[string]bool, len(files))
	for rel, content := range files {
		if err := w.file(root+"/"+rel, content); err != nil {
			return err
		}
		keep[filepath.ToSlash(filepath.Clean(rel))] = true
	}

	base := w.abs(root)
	var dirs []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if keep[rel] {
			return nil
		}
		return w.remove(root + "/" + rel)
	})
	if err != nil {
		return err
	}
	// Deepest first, so a directory is tried after the ones inside it. A
	// directory that still holds something fails to remove, which is the
	// intent, so the error is ignored.
	for i := len(dirs) - 1; i > 0; i-- {
		_ = os.Remove(dirs[i])
	}
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
	if opts.has(AdapterAgentSkills) {
		if err := writeIntegration(w, AdapterAgentSkills, agentSkillsDir); err != nil {
			return w.changed, fmt.Errorf("agent-skills adapter: %w", err)
		}
	}
	return w.changed, nil
}
