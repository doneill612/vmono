package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/render"
)

// InitName is the directory of a package that holds its init workspace. Being
// a dot name it is never taken for a session, is ignored by the package repo,
// and cannot clash with a session, whose names are slugs.
const InitName = ".init"

// initFile records an init workspace inside it, in its .vmono directory.
const initFile = "init.toml"

// InitWorkspace is the temporary checkout an agent explores to initialize a
// package: a detached worktree of every member, whatever its mode. It is not
// a session: it has no branch, lands nothing, and is removed when the harness
// exits. Its file, rather than the manifest, says what there is to remove,
// since the members may change while it is open.
type InitWorkspace struct {
	Pkg *Package `toml:"-"`
	Dir string   `toml:"-"`
	// PID is the vmono process that owns the workspace, or 0 once it is done
	// with it.
	PID     int          `toml:"pid"`
	Started time.Time    `toml:"started"`
	Members []initMember `toml:"member"`
}

// initMember is one worktree of an init workspace.
type initMember struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Path   string `toml:"path"`
	// Head is the commit it was checked out at; empty until that is done.
	Head string `toml:"head,omitempty"`
}

// InitRunningError reports that a package is being initialized right now.
type InitRunningError struct {
	Package string
	PID     int
}

// Error implements error.
func (e *InitRunningError) Error() string {
	return fmt.Sprintf("package %s is being initialized by another vmono (pid %d); exit that harness first", e.Package, e.PID)
}

// InitDir returns where a package's init workspace lives while there is one.
func (p *Package) InitDir() string { return filepath.Join(p.Dir, InitName) }

// Running reports whether the process that owns the workspace is alive.
func (w *InitWorkspace) Running() bool { return w.PID != 0 && processAlive(w.PID) }

// save writes the workspace's file.
func (w *InitWorkspace) save() error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(w); err != nil {
		return err
	}
	file := filepath.Join(w.Dir, manifest.Dir, initFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, buf.Bytes(), 0o644)
}

// FindInit returns the package's init workspace, or nil when it has none. A
// directory with no readable file, as a crash in the middle of creating one
// can leave, comes back as a workspace with no members.
func (c *Core) FindInit(p *Package) *InitWorkspace {
	dir := p.InitDir()
	if _, err := os.Stat(dir); err != nil {
		return nil
	}
	w := &InitWorkspace{}
	if data, err := os.ReadFile(filepath.Join(dir, manifest.Dir, initFile)); err != nil || toml.Unmarshal(data, w) != nil {
		w = &InitWorkspace{}
	}
	w.Pkg, w.Dir = p, dir
	return w
}

// exploredMembers returns the members a record covers: those checked out in
// the package's running init workspace, else the ones it has now.
func (c *Core) exploredMembers(p *Package) []string {
	if w := c.FindInit(p); w != nil && w.Running() {
		out := make([]string, 0, len(w.Members))
		for _, mem := range w.Members {
			out = append(out, mem.Name)
		}
		return out
	}
	return memberNames(p.M)
}

// initView returns the package as an init workspace holds it: every member a
// reference at its base or ref, with nothing to copy or run. The base is used
// rather than a landing branch because what is recorded describes the
// package, not one feature. The package itself is not changed.
func initView(p *Package) *Package {
	m := clone(p.M)
	for i := range m.Members {
		mem := &m.Members[i]
		if mem.Mode == manifest.ModeActive {
			mem.Ref = mem.Base
		}
		mem.Mode, mem.Base = manifest.ModeReference, ""
		mem.Path = manifest.DefaultPath(mem.Name, manifest.ModeReference)
		mem.Copy, mem.Hooks, mem.Setup, mem.Teardown = nil, false, nil, nil
	}
	m.ApplyDefaults()
	return &Package{Name: p.Name, Dir: p.Dir, M: m}
}

// OpenInit checks every member out into a new init workspace. Sessions,
// branches and the manifest are not involved, and neither setup commands nor
// hooks run. A leftover from a run that died is replaced when nothing in it
// was changed, and otherwise refused. If any step fails everything is undone.
func (c *Core) OpenInit(p *Package) (*InitWorkspace, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if len(p.M.Members) == 0 {
		return nil, fmt.Errorf("package %s has no members to explore", p.Name)
	}
	if old := c.FindInit(p); old != nil {
		err := c.CloseInit(old, false)
		if errors.Is(err, ErrRefused) {
			return nil, fmt.Errorf("an earlier initialization of %s left changes in %s; remove it with `vmono package init %s --clean --force`: %w", p.Name, old.Dir, p.Name, err)
		}
		if err != nil {
			return nil, err
		}
	}

	view := initView(p)
	srcs, err := memberSources(view.M)
	if err != nil {
		return nil, err
	}
	if _, err := preflightMembers(view.M, "", srcs, view.M.Members, false, nil); err != nil {
		return nil, err
	}
	w := &InitWorkspace{Pkg: p, Dir: p.InitDir(), PID: os.Getpid(), Started: time.Now().Truncate(time.Second)}
	var list []string
	for _, mem := range view.M.Members {
		list = append(list, srcs[mem.Name])
		w.Members = append(w.Members, initMember{Name: mem.Name, Source: srcs[mem.Name], Path: mem.Path})
	}
	// A session in name only, so the steps that add worktrees can be shared.
	s := &Session{Pkg: view, Name: InitName, Dir: w.Dir, S: &manifest.Session{}}

	steps := append(c.fetchStep(list...), apply.Step{
		Name: "create init workspace",
		Run: func(func(string)) error {
			if err := os.Mkdir(w.Dir, 0o755); err != nil {
				return err
			}
			if err := w.save(); err != nil {
				return err
			}
			return gitx.Init(w.Dir, "main")
		},
		Undo: func() error { return os.RemoveAll(w.Dir) },
	})
	for _, mem := range view.M.Members {
		steps = append(steps, c.addMemberSteps(s, mem, srcs[mem.Name], false)...)
	}
	steps = append(steps, apply.Step{Name: "render files", Run: func(func(string)) error {
		for i := range w.Members {
			head, err := gitx.ResolveRef(filepath.Join(w.Dir, filepath.FromSlash(w.Members[i].Path)), "HEAD")
			if err != nil {
				return err
			}
			w.Members[i].Head = head
		}
		if err := w.save(); err != nil {
			return err
		}
		// The agent is told what commands members have, which the view leaves
		// out so that they do not run.
		shown := clone(view.M)
		for i := range shown.Members {
			if mem, ok := p.M.Member(shown.Members[i].Name); ok {
				shown.Members[i].Setup, shown.Members[i].Teardown = mem.Setup, mem.Teardown
			}
		}
		if err := render.RenderInit(w.Dir, shown, c.renderOptions()); err != nil {
			return err
		}
		_, err := gitx.CommitAll(w.Dir, "vmono: initialize package "+p.Name, "")
		return err
	}})
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps); err != nil {
		return nil, err
	}
	return w, nil
}

// initChanges returns the members of an init workspace that are not as they
// were checked out: edited or untracked files, or another commit. A package's
// usual safety check does not fit, since it counts every commit of a repo
// with no remote as unpushed.
func initChanges(w *InitWorkspace) []MemberSafety {
	if len(w.Members) == 0 {
		return []MemberSafety{{Session: InitName, Name: w.Dir, Error: "not a workspace vmono recognizes"}}
	}
	var out []MemberSafety
	for _, mem := range w.Members {
		dir := filepath.Join(w.Dir, filepath.FromSlash(mem.Path))
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		ms := MemberSafety{Session: InitName, Name: mem.Name, Mode: manifest.ModeReference}
		if st, err := gitx.StatusOf(dir); err != nil {
			ms.Error = err.Error()
		} else {
			ms.Dirty, ms.Untracked = st.Dirty, st.Untracked
		}
		if head, err := gitx.ResolveRef(dir, "HEAD"); err == nil && mem.Head != "" && head != mem.Head {
			n, _ := gitx.RevCount(dir, mem.Head+"..HEAD")
			ms.Unpushed = max(n, 1)
		}
		if ms.Unsafe() {
			out = append(out, ms)
		}
	}
	return out
}

// CloseInit removes an init workspace: its worktrees, their records in the
// source repos, and the directory. One owned by another live process returns
// an *InitRunningError. One with changes is kept and returns a *RefusedError
// unless forced; it is then no longer owned, so it can be cleaned up later.
func (c *Core) CloseInit(w *InitWorkspace, force bool) error {
	if w.PID != os.Getpid() && w.Running() {
		return &InitRunningError{Package: w.Pkg.Name, PID: w.PID}
	}
	if bad := initChanges(w); len(bad) > 0 && !force {
		if len(w.Members) > 0 {
			w.PID = 0
			_ = w.save()
		}
		return &RefusedError{Unsafe: bad}
	}
	if err := checkNotInside(w.Dir); err != nil {
		return err
	}
	steps, err := c.initRemovalSteps(w, force)
	if err != nil {
		return err
	}
	return (&apply.Runner{Events: c.Events}).Run(steps)
}

// initRemovalSteps returns the steps that take an init workspace down. The
// checks are the caller's job.
func (c *Core) initRemovalSteps(w *InitWorkspace, force bool) ([]apply.Step, error) {
	s := &Session{Pkg: w.Pkg, Name: InitName, Dir: w.Dir, S: &manifest.Session{}}
	var steps []apply.Step
	for _, mem := range w.Members {
		if _, err := os.Stat(mem.Source); err != nil {
			continue // nothing left to prune; the directory goes below
		}
		ms, err := c.removeMemberSteps(s, manifest.Member{
			Name: mem.Name, Source: mem.Source, Path: mem.Path, Mode: manifest.ModeReference,
		}, force, false, nil)
		if err != nil {
			return nil, err
		}
		steps = append(steps, ms...)
	}
	return append(steps, apply.Step{Name: "remove init workspace", Run: func(func(string)) error {
		return os.RemoveAll(w.Dir)
	}}), nil
}

// checkInit says whether a package's init workspace, if any, can be removed
// along with the package: an error while it is running, else the members that
// would lose changes.
func (c *Core) checkInit(p *Package) (w *InitWorkspace, bad []MemberSafety, err error) {
	if w = c.FindInit(p); w == nil {
		return nil, nil, nil
	}
	if w.Running() {
		return w, nil, &InitRunningError{Package: p.Name, PID: w.PID}
	}
	return w, initChanges(w), nil
}

// claimInit marks the worktrees of a package's init workspace as vmono's own,
// and reports one left behind by a run that died. With fix it is removed,
// unless something in it was changed.
func (c *Core) claimInit(p *Package, fix bool, claimed map[string]bool, add func(Finding)) {
	w := c.FindInit(p)
	if w == nil {
		return
	}
	for _, mem := range w.Members {
		claimed[realPath(filepath.Join(w.Dir, filepath.FromSlash(mem.Path)))] = true
	}
	if w.Running() {
		return
	}
	f := Finding{Kind: KindStaleInit, Package: p.Name, Message: "an initialization left its workspace behind at " + w.Dir}
	if fix {
		if err := c.CloseInit(w, false); err != nil {
			f.Message += " (could not remove: " + err.Error() + "; `vmono package init " + p.Name + " --clean --force` discards it)"
		} else {
			f.Fixed = true
		}
	}
	add(f)
}
