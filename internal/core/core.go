// Package core holds the operations both the CLI and the TUI call: create,
// status, delete, edit, sync and doctor, for packages and their sessions. Neither front end shells
// out to the other; both drive these functions and read the same progress
// events.
package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Package is a loaded package: its directory and manifest. A package
// defines members and text; the work happens in its sessions.
type Package struct {
	Name string
	Dir  string
	M    *manifest.Manifest
}

// SessionDir returns the directory a session of this package lives in.
func (p *Package) SessionDir(name string) string { return filepath.Join(p.Dir, name) }

// Sessions returns the names of the package's sessions, sorted: the child
// directories that hold a session file. Other directories, such as a stray
// one a person created, are not sessions; doctor reports them.
func (p *Package) Sessions() ([]string, error) {
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(manifest.SessionPath(filepath.Join(p.Dir, e.Name()))); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Session is one unit of work on a package: a directory holding a worktree
// of every member on the session's branch, which is what a harness opens.
type Session struct {
	Pkg  *Package
	Name string
	Dir  string
	S    *manifest.Session
}

// Branch returns the branch every active member of the session is on.
func (s *Session) Branch() string { return s.S.Branch }

// MemberDir returns the absolute worktree directory of a member in this
// session.
func (s *Session) MemberDir(mem manifest.Member) string {
	return filepath.Join(s.Dir, filepath.FromSlash(mem.Path))
}

// Label returns "package/session", the form people type to name one.
func (s *Session) Label() string { return s.Pkg.Name + "/" + s.Name }

// SplitTarget splits a "package" or "package/session" argument.
func SplitTarget(arg string) (pkg, session string) {
	pkg, session, _ = strings.Cut(arg, "/")
	return pkg, session
}

// Load reads the named package. A package from before sessions existed
// fails with an error matching manifest.ErrLegacy.
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

// LoadSession reads one session of a package.
func (c *Core) LoadSession(p *Package, name string) (*Session, error) {
	if err := manifest.ValidateName(name); err != nil {
		return nil, NotFound("package %s has no session %q", p.Name, name)
	}
	dir := p.SessionDir(name)
	ms, err := manifest.LoadSession(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, NotFound("package %s has no session %q", p.Name, name)
	}
	if err != nil {
		return nil, fmt.Errorf("session %s/%s: %w", p.Name, name, err)
	}
	if ms.Package != p.Name || ms.Name != name {
		return nil, fmt.Errorf("session %s/%s: its session file says %s/%s", p.Name, name, ms.Package, ms.Name)
	}
	return &Session{Pkg: p, Name: name, Dir: dir, S: ms}, nil
}

// LoadSessions reads every session of a package, skipping none: a session
// that cannot be read is an error, since callers act on all of them.
func (c *Core) LoadSessions(p *Package) ([]*Session, error) {
	names, err := p.Sessions()
	if err != nil {
		return nil, err
	}
	out := make([]*Session, 0, len(names))
	for _, n := range names {
		s, err := c.LoadSession(p, n)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// packageFromDir loads the package whose directory is dir, as found by
// walking up from the working directory.
func (c *Core) packageFromDir(dir string) (*Package, error) {
	m, err := manifest.Load(dir)
	if err != nil {
		return nil, err
	}
	return &Package{Name: m.Name, Dir: dir, M: m}, nil
}

// ResolvePackage loads the package named by arg, which is "package" or
// "package/session" (the session part is ignored: a package is what is
// wanted). When arg is empty, it is the package containing the working
// directory, which includes being inside one of its sessions, or the one
// named by VMONO_ROOT.
func (c *Core) ResolvePackage(arg string) (*Package, error) {
	if arg != "" {
		name, _ := SplitTarget(arg)
		return c.Load(name)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dir, err := manifest.Find(wd)
	if err != nil {
		return nil, NotFound("not inside a package; name one explicitly")
	}
	return c.packageFromDir(dir)
}

// SessionChoiceError reports that a package argument did not name exactly
// one session. It matches ErrNotFound, and lists the sessions there are so
// the caller can say which to pick.
type SessionChoiceError struct {
	Package  string
	Sessions []string
}

// Error implements error.
func (e *SessionChoiceError) Error() string {
	if len(e.Sessions) == 0 {
		return fmt.Sprintf("%s: package %s has no sessions; create one with `vmono session new %s NAME`", ErrNotFound, e.Package, e.Package)
	}
	return fmt.Sprintf("%s: package %s has %d sessions; name one as %s/NAME (%s)", ErrNotFound, e.Package, len(e.Sessions), e.Package, strings.Join(e.Sessions, ", "))
}

// Is makes errors.Is(err, ErrNotFound) true.
func (e *SessionChoiceError) Is(target error) bool { return target == ErrNotFound }

// soleSession returns the package's only session, or a *SessionChoiceError.
func (c *Core) soleSession(p *Package) (*Session, error) {
	names, err := p.Sessions()
	if err != nil {
		return nil, err
	}
	if len(names) != 1 {
		return nil, &SessionChoiceError{Package: p.Name, Sessions: names}
	}
	return c.LoadSession(p, names[0])
}

// ResolveSession loads the session named by arg, "package/session". A bare
// "package" names its only session, and fails with a *SessionChoiceError if
// there are none or several. An empty arg is the session containing the
// working directory (or named by VMONO_ROOT); from a package directory
// outside any session it falls back to the package's only session.
func (c *Core) ResolveSession(arg string) (*Session, error) {
	if arg != "" {
		pkg, session := SplitTarget(arg)
		p, err := c.Load(pkg)
		if err != nil {
			return nil, err
		}
		if session == "" {
			return c.soleSession(p)
		}
		return c.LoadSession(p, session)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if dir, err := manifest.FindSession(wd); err == nil {
		p, err := c.packageFromDir(filepath.Dir(dir))
		if err != nil {
			return nil, err
		}
		return c.LoadSession(p, filepath.Base(dir))
	}
	p, err := c.ResolvePackage("")
	if err != nil {
		return nil, NotFound("not inside a session; name one as PACKAGE/SESSION")
	}
	return c.soleSession(p)
}

// ResolveContext loads the package a command is about and, when one is
// meant, its session. It is for commands that work on a whole package but
// can also report on or act in one session:
//
//   - "package/session" names both;
//   - "package" names the package only;
//   - an empty arg uses the working directory (or VMONO_ROOT): inside a
//     session it is that session and its package, and in a package directory
//     outside any session it is the package only.
func (c *Core) ResolveContext(arg string) (*Package, *Session, error) {
	switch {
	case strings.Contains(arg, "/"):
		s, err := c.ResolveSession(arg)
		if err != nil {
			return nil, nil, err
		}
		return s.Pkg, s, nil
	case arg != "":
		p, err := c.Load(arg)
		return p, nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	if _, err := manifest.FindSession(wd); err == nil {
		s, err := c.ResolveSession("")
		if err != nil {
			return nil, nil, err
		}
		return s.Pkg, s, nil
	}
	p, err := c.ResolvePackage("")
	return p, nil, err
}

// Summary is one row of the package list.
type Summary struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Harness string `json:"harness,omitempty"`
	Members int    `json:"members"`
	// Active and Reference split Members by mode.
	Active    int `json:"active"`
	Reference int `json:"reference"`
	Sessions  int `json:"sessions"`
	// Legacy marks a package from before sessions existed. It lists with no
	// members or sessions until `vmono doctor --fix` migrates it.
	Legacy bool `json:"legacy,omitempty"`
	// Sources lists each member's source repo as written in the manifest.
	// It is for callers that relate packages to repos and is not part of
	// the JSON output.
	Sources []string `json:"-"`
	// Dirty is true when any member of any session has uncommitted changes.
	Dirty      bool   `json:"dirty"`
	LastOpened string `json:"last_opened,omitempty"`
}

// SessionSummary is one row of a package's session list.
type SessionSummary struct {
	Package    string `json:"package"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	Dirty      bool   `json:"dirty"`
	LastOpened string `json:"last_opened,omitempty"`
}

// timeFormat is how last-opened times are reported.
const timeFormat = "2006-01-02T15:04:05Z07:00"

// List returns every package under the package root, most recently opened
// first. It scans the directory rather than trusting the index. With
// withStatus it also checks each member of each session for uncommitted
// changes, which costs a git call per member.
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
		if errors.Is(err, manifest.ErrLegacy) {
			dir, _ := c.PackageDir(e.Name())
			out = append(out, Summary{Name: e.Name(), Path: dir, Legacy: true})
			continue
		}
		if err != nil {
			continue
		}
		s := Summary{Name: p.Name, Path: p.Dir, Harness: p.M.Harness, Members: len(p.M.Members)}
		for _, mem := range p.M.Members {
			if mem.Mode == manifest.ModeReference {
				s.Reference++
			} else {
				s.Active++
			}
			s.Sources = append(s.Sources, mem.Source)
		}
		sessions, _ := c.LoadSessions(p)
		s.Sessions = len(sessions)
		if ip, ok := c.Idx.Packages[p.Name]; ok && !ip.LastOpened.IsZero() {
			s.LastOpened = ip.LastOpened.Format(timeFormat)
		}
		if withStatus {
			for _, sess := range sessions {
				if c.sessionDirty(sess) {
					s.Dirty = true
					break
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

// sessionDirty reports whether any member of the session has uncommitted or
// untracked files.
func (c *Core) sessionDirty(s *Session) bool {
	for _, ms := range c.Status(s).Members {
		if ms.Dirty > 0 || ms.Untracked > 0 {
			return true
		}
	}
	return false
}

// ListSessions returns a package's sessions, most recently opened first.
// With withStatus it also checks each session for uncommitted changes.
func (c *Core) ListSessions(p *Package, withStatus bool) ([]SessionSummary, error) {
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		row := SessionSummary{Package: p.Name, Name: s.Name, Path: s.Dir, Branch: s.S.Branch}
		if at := c.Idx.SessionOpened(p.Name, s.Name); !at.IsZero() {
			row.LastOpened = at.Format(timeFormat)
		}
		if withStatus {
			row.Dirty = c.sessionDirty(s)
		}
		out = append(out, row)
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

// Render regenerates every generated file in a session, returning the
// relative paths that changed. It never touches git.
func (c *Core) Render(s *Session) ([]string, error) {
	opts := render.Options{Adapters: c.adapters()}
	for _, name := range c.Cfg.HarnessNames() {
		if h := c.Cfg.Harness[name]; h.Adapter == render.AdapterOpenCode {
			opts.OpenCodeV2 = render.OpenCodeV2(h.Command)
			break
		}
	}
	return render.Render(s.Dir, s.Pkg.M, s.S, opts)
}

// RenderAll regenerates the generated files of every session of a package
// and the package's own .gitignore. A session that fails does not stop the
// rest; the errors are joined.
func (c *Core) RenderAll(p *Package) error {
	var errs []error
	if _, err := render.RenderPackage(p.Dir); err != nil {
		errs = append(errs, err)
	}
	sessions, err := c.LoadSessions(p)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, s := range sessions {
		if _, err := c.Render(s); err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", s.Name, err))
		}
	}
	return errors.Join(errs...)
}
