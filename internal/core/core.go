// Package core holds the operations both the CLI and the TUI call: create,
// status, delete and, later, edit, sync and doctor. Neither front end shells
// out to the other; both drive these functions and read the same progress
// events.
package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/index"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/render"
)

// Sentinel errors the CLI maps to exit codes: ErrRefused is 2 (work would
// be lost; --force overrides) and ErrNotFound is 3.
var (
	ErrRefused  = errors.New("refused: work would be lost")
	ErrNotFound = errors.New("not found")
)

// NotFound returns an error that matches ErrNotFound.
func NotFound(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, args...))
}

// Core carries the configuration and state shared by every operation.
type Core struct {
	Cfg config.Config
	Idx *index.Index
	// Author is recorded on shell-repo commits and notes: "user", or the
	// value of VMONO_AUTHOR (e.g. "agent:claude") inside a harness.
	Author string
	// Events receives progress from apply steps. May be nil. The caller
	// owns the channel and must drain it.
	Events chan<- apply.Event
}

// New builds a Core from a loaded config, reading the index cache.
func New(cfg config.Config) (*Core, error) {
	idx, err := index.Load()
	if err != nil {
		return nil, err
	}
	author := os.Getenv("VMONO_AUTHOR")
	if author == "" {
		author = "user"
	}
	return &Core{Cfg: cfg, Idx: idx, Author: author}, nil
}

// PackageRoot returns the absolute package root.
func (c *Core) PackageRoot() (string, error) { return c.Cfg.PackageRootPath() }

// PackageDir returns the directory a package of the given name lives in.
func (c *Core) PackageDir(name string) (string, error) {
	root, err := c.PackageRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// Package is a loaded package: its directory and manifest.
type Package struct {
	Name string
	Dir  string
	M    *manifest.Manifest
}

// MemberDir returns the absolute worktree directory of a member.
func (p *Package) MemberDir(mem manifest.Member) string {
	return filepath.Join(p.Dir, filepath.FromSlash(mem.Path))
}

// Load reads the named package.
func (c *Core) Load(name string) (*Package, error) {
	if err := manifest.ValidateName(name); err != nil {
		return nil, NotFound("no package %q", name)
	}
	dir, err := c.PackageDir(name)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, NotFound("no package %q", name)
	}
	if err != nil {
		return nil, fmt.Errorf("package %s: %w", name, err)
	}
	return &Package{Name: name, Dir: dir, M: m}, nil
}

// ResolvePackage loads the package named by arg, or, when arg is empty,
// the package containing the working directory (or named by VMONO_ROOT).
func (c *Core) ResolvePackage(arg string) (*Package, error) {
	if arg != "" {
		return c.Load(arg)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir, err := manifest.Find(wd)
	if err != nil {
		return nil, NotFound("not inside a package; name one explicitly")
	}
	m, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	return &Package{Name: m.Name, Dir: dir, M: m}, nil
}

// Summary is one row of the package list.
type Summary struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Harness    string `json:"harness,omitempty"`
	Members    int    `json:"members"`
	Dirty      bool   `json:"dirty"`
	LastOpened string `json:"last_opened,omitempty"`
}

// List returns every package under the package root, most recently opened
// first. It scans the directory rather than trusting the index. With
// withStatus it also checks each member for uncommitted changes, which
// costs a git call per member.
func (c *Core) List(withStatus bool) ([]Summary, error) {
	root, err := c.PackageRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Summary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := c.Load(e.Name())
		if err != nil {
			continue
		}
		s := Summary{Name: p.Name, Path: p.Dir, Branch: p.M.Branch, Harness: p.M.Harness, Members: len(p.M.Members)}
		if ip, ok := c.Idx.Packages[p.Name]; ok && !ip.LastOpened.IsZero() {
			s.LastOpened = ip.LastOpened.Format("2006-01-02T15:04:05Z07:00")
		}
		if withStatus {
			for _, ms := range c.Status(p).Members {
				if ms.Dirty > 0 || ms.Untracked > 0 {
					s.Dirty = true
				}
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastOpened != out[j].LastOpened {
			return out[i].LastOpened > out[j].LastOpened
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Repos lists every known source repo: under source roots, in config, or
// added by path before. The package root is never scanned.
func (c *Core) Repos() ([]discover.Repo, error) {
	roots, err := c.Cfg.SourceRootPaths()
	if err != nil {
		return nil, err
	}
	var configured []string
	for _, r := range c.Cfg.Repos {
		p, err := paths.Abs(r)
		if err != nil {
			return nil, err
		}
		configured = append(configured, p)
	}
	pkgRoot, err := c.PackageRoot()
	if err != nil {
		return nil, err
	}
	return discover.Repos(roots, configured, c.Idx, c.Cfg.ScanDepth, []string{pkgRoot}), nil
}

// ResolveRepo turns a repo name or a path into a repo. A path-resolved repo
// is remembered in the index so it shows in the picker afterwards.
func (c *Core) ResolveRepo(arg string) (discover.Repo, error) {
	known, err := c.Repos()
	if err != nil {
		return discover.Repo{}, err
	}
	r, err := discover.Resolve(arg, known)
	if err != nil {
		var nf *discover.NotFoundError
		if errors.As(err, &nf) {
			return discover.Repo{}, fmt.Errorf("%w: %s", ErrNotFound, err.Error())
		}
		return discover.Repo{}, err
	}
	if r.Origin == "path" {
		if c.Idx.AddRepo(index.Repo{Path: r.Path, Remote: r.Remote}) {
			_ = c.Idx.Save()
		}
	}
	return r, nil
}

// adapters returns the distinct adapter names used by configured harnesses.
func (c *Core) adapters() []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range c.Cfg.HarnessNames() {
		a := c.Cfg.Harness[name].Adapter
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

// Render regenerates every generated file in a package, returning the
// relative paths that changed. It never touches git.
func (c *Core) Render(p *Package) ([]string, error) {
	opts := render.Options{Adapters: c.adapters()}
	for _, name := range c.Cfg.HarnessNames() {
		if h := c.Cfg.Harness[name]; h.Adapter == render.AdapterOpenCode {
			opts.OpenCodeV2 = render.OpenCodeV2(h.Command)
			break
		}
	}
	return render.Render(p.Dir, p.M, opts)
}
