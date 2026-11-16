package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/render"
)

// legacyGenerated lists the generated files and directories a schema 1
// package kept at its root. In the session layout they belong to a session,
// so migration moves them with the worktrees. AGENTS.md is among them: its
// text outside the vmono markers is the user's and must survive.
var legacyGenerated = []string{"AGENTS.md", "CLAUDE.md", ".claude", ".opencode", "opencode.json"}

// legacySessionName picks the name of the session a legacy package becomes:
// "main", or "main-2" and so on when that name is taken by a member's
// top-level directory or anything else already in the package directory.
func legacySessionName(dir string, m *manifest.Manifest) string {
	taken := map[string]bool{}
	for _, mem := range m.Members {
		taken[strings.SplitN(filepath.ToSlash(mem.Path), "/", 2)[0]] = true
	}
	for i := 1; ; i++ {
		name := "main"
		if i > 1 {
			name = fmt.Sprintf("main-%d", i)
		}
		if taken[name] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			continue
		}
		return name
	}
}

// moveStep returns a step that renames from to to, and back on undo. A
// missing from is not an error: there is nothing to move (a worktree deleted
// by hand, a generated file never written), and sync rebuilds what matters.
func moveStep(member, name, from, to string, after func(moved bool) error, undoAfter func() error) apply.Step {
	moved := false
	return apply.Step{
		Member: member,
		Name:   name,
		Run: func(func(string)) error {
			if _, err := os.Lstat(from); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			if err := os.Rename(from, to); err != nil {
				return err
			}
			moved = true
			if after != nil {
				return after(true)
			}
			return nil
		},
		Undo: func() error {
			if !moved {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(from), 0o755); err != nil {
				return err
			}
			if err := os.Rename(to, from); err != nil {
				return err
			}
			if undoAfter != nil {
				return undoAfter()
			}
			return nil
		},
	}
}

// migratePackage turns a schema 1 package, whose branch and worktrees lived
// in the package itself, into a package with one session named "main". The
// session keeps the legacy branch, so the work on it is not renamed. The
// steps are:
//
//  1. Each member's worktree directory moves into the session directory, and
//     git's link to it in the source repo is repaired.
//  2. The generated files move too, keeping any user text in AGENTS.md.
//  3. The session gets its own git repo and session file, and its generated
//     files are rendered and committed.
//  4. The package manifest is rewritten as schema 2, and committed in the
//     package's existing repo, which keeps the history of its text.
//  5. The package's last-opened time carries over to the session.
//
// Every step is undone if a later one fails, except the final commits.
// Harness history keyed by the old path, which Claude Code uses, does not
// follow the move.
func (c *Core) migratePackage(name string) error {
	dir, err := c.PackageDir(name)
	if err != nil {
		return err
	}
	m, branch, err := manifest.LoadLegacy(dir)
	if err != nil {
		return err
	}
	origManifest, err := os.ReadFile(manifest.Path(dir))
	if err != nil {
		return err
	}
	sessName := legacySessionName(dir, m)
	sessDir := filepath.Join(dir, sessName)
	ms := &manifest.Session{
		Schema: manifest.SessionSchemaVersion, Package: name, Name: sessName,
		Created: m.Created, Branch: branch,
	}
	p := &Package{Name: name, Dir: dir, M: m}
	s := &Session{Pkg: p, Name: sessName, Dir: sessDir, S: ms}

	steps := []apply.Step{{
		Name: "create session",
		Run:  func(func(string)) error { return os.Mkdir(sessDir, 0o755) },
		Undo: func() error { return os.Remove(sessDir) },
	}}

	for _, mem := range m.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return err
		}
		rel := filepath.FromSlash(mem.Path)
		from, to := filepath.Join(dir, rel), filepath.Join(sessDir, rel)
		steps = append(steps, moveStep(mem.Name, "move worktree", from, to,
			func(bool) error { return repairWorktree(src, to) },
			func() error { return repairWorktree(src, from) },
		))
	}
	steps = append(steps, apply.Step{Name: "tidy", Run: func(func(string)) error {
		// Reference members lived under _ref/, now empty.
		_ = os.Remove(filepath.Join(dir, "_ref"))
		return nil
	}})
	for _, f := range legacyGenerated {
		steps = append(steps, moveStep("", "move "+f, filepath.Join(dir, f), filepath.Join(sessDir, f), nil, nil))
	}

	steps = append(steps,
		apply.Step{Name: "init session repo", Run: func(func(string)) error {
			if err := gitx.Init(sessDir, "main"); err != nil {
				return err
			}
			return manifest.SaveSession(sessDir, ms)
		}},
		apply.Step{Name: "render session files", Run: func(func(string)) error {
			_, err := c.Render(s)
			return err
		}},
		apply.Step{Name: "commit session", Run: func(func(string)) error {
			_, err := gitx.CommitAll(sessDir, "vmono: migrate "+name+" to sessions", "")
			return err
		}},
		apply.Step{
			Name: "rewrite manifest",
			Run: func(func(string)) error {
				if err := manifest.Save(dir, m); err != nil {
					return err
				}
				_, err := render.RenderPackage(dir)
				return err
			},
			Undo: func() error { return os.WriteFile(manifest.Path(dir), origManifest, 0o644) },
		},
		apply.Step{Name: "commit package", Run: func(func(string)) error {
			_, err := gitx.CommitAll(dir, fmt.Sprintf("vmono: migrate to sessions (%s)", c.Author), "")
			return err
		}},
	)
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps); err != nil {
		return err
	}

	if ip, ok := c.Idx.Packages[name]; ok && !ip.LastOpened.IsZero() {
		if ip.Sessions == nil {
			ip.Sessions = map[string]time.Time{}
		}
		ip.Sessions[sessName] = ip.LastOpened
		c.Idx.Packages[name] = ip
	}
	c.Idx.SetPackage(name, dir)
	_ = c.Idx.Save()
	return nil
}

// repairWorktree tells a source repo that a worktree directory moved. A
// source that is gone is not an error here: the worktree is moved anyway and
// doctor reports the missing source.
func repairWorktree(src, path string) error {
	if _, err := os.Stat(src); err != nil {
		return nil
	}
	return gitx.WorktreeRepair(src, path)
}
