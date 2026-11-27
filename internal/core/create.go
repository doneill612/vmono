package core

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/render"
)

// SessionOptions tunes CreateSession. A member's branch is the one named for it
// in BranchByMember or TrackByMember, else the one named for all of them in
// Branch or Track, else the session's name, after Prefix and a slash when there
// is one.
type SessionOptions struct {
	// Prefix comes before the session's name in the default branch.
	Prefix string
	// Branch names every active member's branch, and BranchByMember
	// individual members'.
	Branch         string
	BranchByMember map[string]string
	// ReuseBranch checks out a branch that already exists in a source repo
	// instead of failing. The CLI requires --reuse-branch; the TUI asks and
	// retries on BranchExistsError.
	ReuseBranch bool
	// Track names a branch on the members' remotes to work on, checked out as a
	// local branch that tracks it. An active member whose remote lacks it gets a
	// new branch of that name, and it fails when none has it. TrackByMember
	// names one for individual members, and fails for one whose remote lacks it.
	Track         string
	TrackByMember map[string]string
	// From names a branch or ref every active member's new branch is built on
	// in place of the member's base, for work stacked on work that has not
	// merged. A member without it is built on its base, and it fails when none
	// has it. FromByMember names one for individual members, and fails for one
	// without it. Both are ignored for a member that tracks a branch.
	//
	// FromByMember also names what a reference member is checked out at in place
	// of the package's ref. From leaves reference members alone.
	From         string
	FromByMember map[string]string
}

// check rejects options that contradict each other or name a member p does not
// have, or one a branch means nothing for.
func (o SessionOptions) check(p *Package) error {
	switch {
	case o.Branch != "" && o.Track != "":
		return errors.New("--branch and --track cannot both name a branch for every member")
	case o.Prefix != "" && (o.Branch != "" || o.Track != ""):
		return errors.New("--prefix belongs to the default branch, so it cannot be given with a branch for every member")
	case o.Track != "" && o.From != "":
		return errors.New("--track and --from cannot both be given for every member: a tracked branch starts where it is")
	}
	for flag, by := range map[string]map[string]string{"--branch": o.BranchByMember, "--track": o.TrackByMember, "--from": o.FromByMember} {
		for member := range by {
			mem, ok := p.M.Member(member)
			switch {
			case !ok:
				return fmt.Errorf("%s names %q, which is not a member of %s", flag, member, p.Name)
			case mem.Mode != manifest.ModeActive && flag != "--from":
				return fmt.Errorf("%s names %q, which is not an active member of %s; choose what a reference member is checked out at with --from %s=REF", flag, member, p.Name, member)
			}
		}
	}
	for member := range o.TrackByMember {
		if o.BranchByMember[member] != "" {
			return fmt.Errorf("--track and --branch both name a branch for %s", member)
		}
		if o.FromByMember[member] != "" {
			return fmt.Errorf("--track and --from are both given for %s: a tracked branch starts where it is", member)
		}
	}
	return nil
}

// refreshes reports whether the options name something that may have appeared
// on a remote since the last fetch.
func (o SessionOptions) refreshes() bool {
	return o.Track != "" || len(o.TrackByMember) > 0 || o.From != "" || len(o.FromByMember) > 0
}

// trackFor returns the remote branch the member is to work on, "" for none, and
// whether it was named for that member.
func (o SessionOptions) trackFor(member string) (branch string, named bool) {
	if b := o.TrackByMember[member]; b != "" {
		return b, true
	}
	if o.BranchByMember[member] != "" {
		return "", false
	}
	return o.Track, false
}

// fromFor returns what the member's branch is to be built on, "" for its base,
// and whether it was named for that member.
func (o SessionOptions) fromFor(member string) (from string, named bool) {
	if f := o.FromByMember[member]; f != "" {
		return f, true
	}
	return o.From, false
}

// session builds the session file the options describe, before anything that
// depends on the repos is known.
func (o SessionOptions) session(p *Package, name string) *manifest.Session {
	branch := firstNonEmpty(o.Branch, o.Track, manifest.DefaultBranch(o.Prefix, name))
	ms := &manifest.Session{Package: p.Name, Name: name, Branch: branch, Created: time.Now().Truncate(time.Second)}
	ms.ApplyDefaults()
	for _, mem := range p.M.Members {
		if b := firstNonEmpty(o.BranchByMember[mem.Name], o.TrackByMember[mem.Name]); b != "" {
			ms.SetMember(mem.Name, manifest.SessionMember{Branch: b})
		}
	}
	return ms
}

// resolveTrack checks that the branches to track are there, as of the last
// fetch, and returns the members that have theirs.
func resolveTrack(p *Package, opts SessionOptions, srcs map[string]string, warn func(member, msg string)) (map[string]bool, error) {
	tracked := map[string]bool{}
	var missing []string
	asked := false
	for _, mem := range p.M.Members {
		branch, named := opts.trackFor(mem.Name)
		if mem.Mode != manifest.ModeActive || branch == "" {
			continue
		}
		asked = asked || !named
		remote := primaryRemote(srcs[mem.Name])
		if remote != "" && branch != "HEAD" && gitx.RefExists(srcs[mem.Name], "refs/remotes/"+remote+"/"+branch) {
			tracked[mem.Name] = true
			continue
		}
		if named {
			return nil, NotFound("%s: no branch %q on %s", mem.Name, branch, firstNonEmpty(remote, "any remote"))
		}
		missing = append(missing, mem.Name)
	}
	if asked && len(missing) > 0 {
		found := false
		for member := range tracked {
			_, named := opts.trackFor(member)
			found = found || !named
		}
		if !found {
			return nil, NotFound("no member of %s has a branch %q on its remote", p.Name, opts.Track)
		}
	}
	for _, member := range missing {
		warn(member, fmt.Sprintf("no branch %q on its remote; starting a new one of that name", opts.Track))
	}
	return tracked, nil
}

// resolveFroms records in ms what each member is built on in place of its base,
// and what each reference member is checked out at in place of the package's
// ref, as of the last fetch. Members in tracked are left out.
func resolveFroms(p *Package, ms *manifest.Session, opts SessionOptions, srcs map[string]string, tracked map[string]bool, warn func(member, msg string)) error {
	asked, found := false, false
	var missing []string
	for _, mem := range p.M.Members {
		from, named := opts.fromFor(mem.Name)
		reference := mem.Mode == manifest.ModeReference
		if from == "" || tracked[mem.Name] || (reference && !named) {
			continue
		}
		asked = asked || !named
		if !reference && from == ms.BranchFor(mem.Name) {
			return fmt.Errorf("%s: %q cannot be built on itself", mem.Name, from)
		}
		src := srcs[mem.Name]
		ref := resolveFrom(src, from)
		if ref == "" {
			if named {
				return NotFound("%s: nothing named %q to start from", mem.Name, from)
			}
			missing = append(missing, mem.Name)
			continue
		}
		found = found || !named
		tip, err := gitx.ResolveRef(src, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", mem.Name, err)
		}
		rec := ms.Members[mem.Name]
		rec.BaseTip = tip
		if reference {
			rec.Ref = from
		} else {
			rec.From = from
		}
		ms.SetMember(mem.Name, rec)
	}
	if asked && !found {
		return NotFound("no member of %s has anything named %q to start from", p.Name, opts.From)
	}
	for _, member := range missing {
		warn(member, fmt.Sprintf("nothing named %q; starting from its base", opts.From))
	}
	return nil
}

// BranchExistsError reports that the session branch already exists in a
// source repo. Callers may retry with SessionOptions.ReuseBranch.
type BranchExistsError struct {
	Member string
	Branch string
}

// Error implements error.
func (e *BranchExistsError) Error() string {
	return fmt.Sprintf("%s: branch %q already exists in the source repo (use --reuse-branch to check it out)", e.Member, e.Branch)
}

// RemoteBranchExistsError reports that a branch about to be started is already
// on the member's remote, where pushing it would meet someone else's work.
type RemoteBranchExistsError struct {
	Member string
	Remote string
	Branch string
}

// Error implements error.
func (e *RemoteBranchExistsError) Error() string {
	return fmt.Sprintf("%s: branch %q is already on %s; work on it with --track, or choose another name with --branch or --prefix", e.Member, e.Branch, e.Remote)
}

// prepareMember resolves one member's source to an absolute repo root,
// defaults its name and remote, and stores the source contracted. It
// returns the absolute source path. It does not apply path/base defaults,
// which need the whole manifest.
func prepareMember(mem *manifest.Member) (string, error) {
	if mem.Source == "" {
		return "", errors.New("member has no source")
	}
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return "", err
	}
	top, bare, err := gitx.WorkTree(src)
	if err != nil {
		return "", NotFound("%s is not a git repository", src)
	}
	if bare {
		return "", fmt.Errorf("%s is a bare repository; members must be non-bare", top)
	}
	if mem.Name == "" {
		mem.Name = manifest.Slugify(filepath.Base(top))
	}
	if mem.Remote == "" {
		mem.Remote = gitx.RemoteURL(top, "origin")
	}
	mem.Source = paths.Contract(top)
	return top, nil
}

// prepareMembers prepares every member of m and applies manifest defaults.
// It returns the absolute source path per member name. It mutates m.
func (c *Core) prepareMembers(m *manifest.Manifest) (map[string]string, error) {
	abs := map[string]string{}
	for i := range m.Members {
		top, err := prepareMember(&m.Members[i])
		if err != nil {
			return nil, fmt.Errorf("member %d: %w", i+1, err)
		}
		abs[m.Members[i].Name] = top
	}
	m.ApplyDefaults()
	return abs, nil
}

// memberSources resolves every member's source of an existing manifest to an
// absolute path, keyed by member name.
func memberSources(m *manifest.Manifest) (map[string]string, error) {
	srcs := make(map[string]string, len(m.Members))
	for _, mem := range m.Members {
		abs, err := paths.Abs(mem.Source)
		if err != nil {
			return nil, err
		}
		srcs[mem.Name] = abs
	}
	return srcs, nil
}

// checkDistinctSources rejects two active members that share a repo: one
// branch can only be checked out once, so a session could not hold both.
func checkDistinctSources(m *manifest.Manifest, srcs map[string]string) error {
	activeSources := map[string]string{}
	for _, mem := range m.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		src := srcs[mem.Name]
		if other, dup := activeSources[src]; dup {
			return fmt.Errorf("members %s and %s are the same repo; one branch can only be checked out once", other, mem.Name)
		}
		activeSources[src] = mem.Name
	}
	return nil
}

// realPath cleans a path and resolves symlinks so paths git reports (which
// are resolved, e.g. /private/var on macOS) compare equal to ones vmono
// built. A path that does not exist yet is resolved through its deepest
// existing ancestor, so a missing worktree directory still compares equal
// to the registration git holds for it.
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// resolveRef returns the ref to use for a member's base or ref, falling
// back to HEAD with a warning when origin/HEAD does not exist. A repo with no
// commits at all gets its own error, since the generic "ref not found" gives
// no hint that the fix is an initial commit rather than a different ref.
func resolveRef(src, ref string, warn func(string)) (string, error) {
	return resolveRefIn(gitx.LiveRefs(src), src, ref, warn)
}

// resolveRefIn is resolveRef answered from refs.
func resolveRefIn(refs gitx.Refs, src, ref string, warn func(string)) (string, error) {
	if ref == "" {
		ref = "origin/HEAD"
	}
	if refs.Exists(ref) {
		return ref, nil
	}
	resolved, fell := refs.ResolveDefault(ref)
	if fell && warn != nil {
		warn("no origin/HEAD; using the source's current HEAD instead")
	}
	if !refs.Exists(resolved) {
		if !gitx.HasAnyCommit(src) {
			return "", fmt.Errorf("%s has no commits yet; make an initial commit there before starting a session on it", src)
		}
		return "", fmt.Errorf("ref %q not found in %s", resolved, src)
	}
	return resolved, nil
}

// preflightMembers checks the members in check before anything touches
// disk, for a session whose members are on the branches branchFor gives: sources
// are usable, no two active members share a repo, the branch is not checked out
// elsewhere, is valid, is not the one the member's work merges into, and does
// not collide with another branch's ref path, and base/ref resolve. srcs maps
// every member of m (not only check) to its absolute source. It returns the
// members whose branch already exists in their source, which fails unless reuse
// allows it.
func preflightMembers(m *manifest.Manifest, branchFor func(member string) string, srcs map[string]string, check []manifest.Member, reuse func(member string) bool, removing map[string]bool) (existing map[string]bool, err error) {
	existing = map[string]bool{}
	if err := checkDistinctSources(m, srcs); err != nil {
		return nil, err
	}
	for _, mem := range check {
		src := srcs[mem.Name]
		switch mem.Mode {
		case manifest.ModeActive:
			branch := branchFor(mem.Name)
			if !gitx.ValidBranchName(src, branch) {
				return nil, fmt.Errorf("%s: %q is not a valid branch name", mem.Name, branch)
			}
			if branch == baseBranchOf(src, mem) {
				return nil, fmt.Errorf("%s: %q is the branch its work merges into, so a session cannot work on it; choose another name with --branch or --prefix", mem.Name, branch)
			}
			if at, err := gitx.BranchCheckedOutAt(src, branch); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			} else if at != "" && !removing[realPath(at)] {
				return nil, fmt.Errorf("%s: branch %q is already checked out at %s; choose another name with --branch or --prefix", mem.Name, branch, at)
			}
			if gitx.BranchExists(src, branch) {
				if !reuse(mem.Name) {
					return nil, &BranchExistsError{Member: mem.Name, Branch: branch}
				}
				existing[mem.Name] = true
			} else {
				if other, clash := gitx.BranchRefConflict(src, branch); clash {
					return nil, fmt.Errorf("%s: branch %q conflicts with existing branch %q (git cannot hold both); pass a different branch name, or rename %q with `git branch -m`", mem.Name, branch, other, other)
				}
				if _, err := resolveRef(src, mem.Base, nil); err != nil {
					return nil, fmt.Errorf("%s: %w", mem.Name, err)
				}
			}
		case manifest.ModeReference:
			if _, err := resolveRef(src, mem.Ref, nil); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			}
		}
	}
	return existing, nil
}

// preflightRemotes rejects a branch that would be new but is already on the
// member's remote as of the last fetch, for the members in check that are not
// in have.
func preflightRemotes(branchFor func(member string) string, check []manifest.Member, srcs map[string]string, have ...map[string]bool) error {
	for _, mem := range check {
		if mem.Mode != manifest.ModeActive || slices.ContainsFunc(have, func(m map[string]bool) bool { return m[mem.Name] }) {
			continue
		}
		branch := branchFor(mem.Name)
		if remote := primaryRemote(srcs[mem.Name]); remote != "" && gitx.RefExists(srcs[mem.Name], "refs/remotes/"+remote+"/"+branch) {
			return &RemoteBranchExistsError{Member: mem.Name, Remote: remote, Branch: branch}
		}
	}
	return nil
}

// baseBranchOf returns the name of the branch mem's base stands for: the one
// the base names, else the source's default branch.
func baseBranchOf(src string, mem manifest.Member) string {
	if b := manifest.BaseBranch(mem); b != "" {
		return b
	}
	return gitx.DefaultBranch(src)
}

// CreatePackage defines a new package from a manifest whose members name
// their sources: it creates the package directory, its own git repo, the
// manifest and a .gitignore, and commits them. It makes no worktrees; the
// work happens in sessions, made with CreateSession. If any step fails, every
// step already done is undone.
//
// The manifest needs Name and Members (Source and Mode each); everything
// else is defaulted. The package repo always gets an initial commit, even
// with auto_commit off.
func (c *Core) CreatePackage(m *manifest.Manifest) (*Package, error) {
	return c.createPackage(m, "vmono: create "+m.Name)
}

// SetMode changes mem's mode as manifest.Member.SetMode does, and drops a ref
// that would become the base of an active member without naming a branch, such
// as a tag or a commit, leaving the default base. The returned note says so, and
// is "" when everything carried over.
func SetMode(mem *manifest.Member, mode string) (note string) {
	was := mem.Mode
	mem.SetMode(mode)
	if was == mode || mode != manifest.ModeActive || namesBranch(mem.Source, mem.Base) {
		return ""
	}
	note = fmt.Sprintf("%s: %s is not a branch, so its base is origin/HEAD", mem.Name, mem.Base)
	mem.Base = ""
	return note
}

// namesBranch reports whether work in the repo at source can merge into what ref
// names: the default branch, which "", HEAD and origin/HEAD stand for, a local
// branch or a remote-tracking one.
func namesBranch(source, ref string) bool {
	switch ref {
	case "", "HEAD", "origin/HEAD":
		return true
	}
	src, err := paths.Abs(source)
	if err != nil {
		return false
	}
	return gitx.BranchExists(src, ref) ||
		gitx.ValidBranchName(src, ref) && gitx.RefExists(src, "refs/remotes/"+ref)
}

// CloneManifest returns a copy of src's manifest under a new name, with
// everything else src records, init and notes included. modes sets the mode of
// the members it names, and notes are what SetMode had to say about them. It
// fails for a name that is not a member of src or a mode that does not exist. src
// is left unchanged, and the name is checked when the package is created.
func CloneManifest(src *Package, name string, modes map[string]string) (m *manifest.Manifest, notes []string, err error) {
	m, err = manifest.Parse(manifest.Encode(src.M))
	if err != nil {
		return nil, nil, err
	}
	m.Name = name
	m.Created = time.Time{}
	for _, member := range slices.Sorted(maps.Keys(modes)) {
		mem, ok := m.Member(member)
		if !ok {
			return nil, nil, NotFound("%s has no member %q", src.Name, member)
		}
		mode := modes[member]
		if mode != manifest.ModeActive && mode != manifest.ModeReference {
			return nil, nil, fmt.Errorf("%s: mode must be %q or %q", member, manifest.ModeActive, manifest.ModeReference)
		}
		if note := SetMode(mem, mode); note != "" {
			notes = append(notes, note)
		}
	}
	m.ApplyDefaults()
	return m, notes, nil
}

// ClonePackage defines a new package from m, a manifest made from src's with
// CloneManifest and possibly edited since. It does what CreatePackage does, and
// records in the new package's history where it came from. Sessions are not
// copied.
func (c *Core) ClonePackage(src *Package, m *manifest.Manifest) (*Package, error) {
	return c.createPackage(m, "vmono: clone "+src.Name+" as "+m.Name)
}

// createPackage is CreatePackage with the message of the first commit.
func (c *Core) createPackage(m *manifest.Manifest, message string) (*Package, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if err := manifest.ValidateName(m.Name); err != nil {
		return nil, err
	}
	if m.Created.IsZero() {
		m.Created = time.Now().Truncate(time.Second)
	}
	srcs, err := c.prepareMembers(m)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := checkDistinctSources(m, srcs); err != nil {
		return nil, err
	}
	dir, err := c.PackageDir(m.Name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("package %q already exists at %s", m.Name, dir)
	}

	p := &Package{Name: m.Name, Dir: dir, M: m}
	steps := []apply.Step{
		{
			Name: "create package",
			Run: func(func(string)) error {
				if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
					return err
				}
				if err := os.Mkdir(dir, 0o755); err != nil {
					return err
				}
				if err := gitx.Init(dir, "main"); err != nil {
					return err
				}
				return manifest.Save(dir, m)
			},
			Undo: func() error { return os.RemoveAll(dir) },
		},
		{Name: "render files", Run: func(func(string)) error {
			_, err := render.RenderPackage(dir)
			return err
		}},
		{Name: "commit", Run: func(func(string)) error {
			_, err := gitx.CommitAll(dir, message, "")
			return err
		}},
	}
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps); err != nil {
		return nil, err
	}
	c.Idx.SetPackage(m.Name, dir)
	_ = c.Idx.Save()
	return p, nil
}

// CreateSession starts a new session of a package: it validates, then makes
// the session directory with its own git repo and session file, a worktree
// of every member on the session branch, copies each member's untracked
// files, renders the generated files, commits them and runs the post_create
// hook. If any step fails,
// every step already done is undone and the error names the failing member.
//
// The session repo always gets an initial commit, even with auto_commit off,
// because OpenCode identifies a project by it: that is what makes each
// session its own project.
func (c *Core) CreateSession(p *Package, name string, opts SessionOptions) (*Session, error) {
	if err := gitx.CheckVersion(); err != nil {
		return nil, err
	}
	if err := manifest.ValidateSessionName(name); err != nil {
		return nil, err
	}
	if err := opts.check(p); err != nil {
		return nil, err
	}
	ms := opts.session(p, name)
	if err := ms.Validate(); err != nil {
		return nil, err
	}
	taken, err := p.Sessions()
	if err != nil {
		return nil, err
	}
	for _, other := range taken {
		if other != name && strings.EqualFold(other, name) {
			return nil, fmt.Errorf("package %s already has a session %q, and a name differing only in case would share its directory on some filesystems", p.Name, other)
		}
	}
	dir := p.SessionDir(name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("package %s already has a session %q at %s", p.Name, name, dir)
	}
	srcs, err := memberSources(p.M)
	if err != nil {
		return nil, err
	}
	var srcList []string
	for _, mem := range p.M.Members {
		srcList = append(srcList, srcs[mem.Name])
	}
	existing, err := preflightMembers(p.M, ms.BranchFor, srcs, p.M.Members, func(member string) bool {
		track, _ := opts.trackFor(member)
		return opts.ReuseBranch || track != ""
	}, nil)
	if err != nil {
		return nil, err
	}
	// What is on the remotes decides where branches start and which names are
	// taken, so the fetch comes ahead of the rest rather than as a step of it.
	if opts.refreshes() || c.Cfg.FetchOnCreate {
		if err := (&apply.Runner{Events: c.Events}).Run([]apply.Step{{Name: "fetch", Run: func(warn func(string)) error {
			fetchSources(srcList, warn)
			return nil
		}}}); err != nil {
			return nil, err
		}
	}
	warn := func(step string) func(member, msg string) {
		return func(member, msg string) { c.warnEvent(member, step, msg) }
	}
	tracked, err := resolveTrack(p, opts, srcs, warn("track"))
	if err != nil {
		return nil, err
	}
	if err := resolveFroms(p, ms, opts, srcs, tracked, warn("from")); err != nil {
		return nil, err
	}
	if err := preflightRemotes(ms.BranchFor, p.M.Members, srcs, existing, tracked); err != nil {
		return nil, err
	}
	for _, mem := range p.M.Members {
		if mem.Mode == manifest.ModeActive && !existing[mem.Name] {
			rec := ms.Members[mem.Name]
			rec.Created = true
			ms.SetMember(mem.Name, rec)
		}
	}
	if err := ms.Validate(); err != nil {
		return nil, err
	}

	s := &Session{Pkg: p, Name: name, Dir: dir, S: ms}
	steps := []apply.Step{{
		Name: "create session",
		Run: func(func(string)) error {
			if err := os.Mkdir(dir, 0o755); err != nil {
				return err
			}
			if err := gitx.Init(dir, "main"); err != nil {
				return err
			}
			return manifest.SaveSession(dir, ms)
		},
		Undo: func() error { return os.RemoveAll(dir) },
	}}
	for _, mem := range p.M.Members {
		steps = append(steps, c.addMemberSteps(s, mem, srcs[mem.Name], branchPlan{exists: existing[mem.Name], forward: tracked[mem.Name]})...)
	}
	steps = append(steps,
		apply.Step{Name: "render files", Run: func(func(string)) error {
			_, err := c.Render(s)
			return err
		}},
		apply.Step{Name: "commit", Run: func(func(string)) error {
			_, err := gitx.CommitAll(dir, "vmono: create session "+name, "")
			return err
		}},
	)
	steps = append(steps, c.hookSteps(config.HookPostCreate, s, nil)...)
	if err := (&apply.Runner{Events: c.Events, Rollback: true}).Run(steps); err != nil {
		return nil, err
	}
	return s, nil
}

// fetchSources fetches the distinct repos behind srcs concurrently and
// reports problems through warn. A failed fetch is never fatal: the caller
// carries on with the refs already on disk. Failures caused by missing
// credentials are folded into one message with the fix, since the same
// advice applies to every repo; any other failure gets its own message.
func fetchSources(srcs []string, warn func(string)) {
	var unauthenticated []string
	for _, r := range gitx.FetchMany(srcs, gitx.DefaultFetchConcurrency) {
		switch {
		case r.Err == nil:
		case gitx.IsAuthError(r.Err):
			unauthenticated = append(unauthenticated, r.Repo)
		default:
			warn(fmt.Sprintf("fetch failed for %s, continuing with local refs: %s", r.Repo, oneLine(r.Err.Error())))
		}
	}
	if len(unauthenticated) > 0 {
		warn(fmt.Sprintf("couldn't fetch %s, continuing with local refs; %s", strings.Join(unauthenticated, ", "), gitx.AuthHint))
	}
}

// oneLine collapses runs of whitespace, newlines included, so git's
// multi-line stderr fits in a single progress row.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// releaseStaleWorktrees prunes repo's worktree records whose directory is
// gone and warns about each. Git refuses to add a worktree at a path, or on
// a branch, that a missing worktree still holds, which is the state a
// package deleted by hand leaves behind in its source repos. Only records
// for directories that no longer exist are dropped, so nothing live is
// touched.
func releaseStaleWorktrees(src string, warn func(string)) error {
	gone, err := gitx.PruneStale(src)
	if err != nil {
		return err
	}
	for _, p := range gone {
		warn("pruned stale worktree record for " + p)
	}
	return nil
}

// fetchStep returns the single package-level step that fetches every repo
// in srcs at once ahead of the member steps, or nothing when fetch_on_create
// is off or there is nothing to fetch. Fetching in one step, not one per
// member, runs the fetches concurrently and fetches a repo shared by
// several members only once.
func (c *Core) fetchStep(srcs ...string) []apply.Step {
	if !c.Cfg.FetchOnCreate || len(srcs) == 0 {
		return nil
	}
	return []apply.Step{{Name: "fetch", Run: func(warn func(string)) error {
		fetchSources(srcs, warn)
		return nil
	}}}
}

// branchPlan is how an active member's branch comes to be checked out.
type branchPlan struct {
	// exists is true when the branch is already there in the source.
	exists bool
	// forward brings a branch that exists up to its remote copy when all it
	// lacks is the copy's newer commits, and makes it track the copy.
	forward bool
}

// addMemberSteps returns the apply steps that bring one member's worktree in
// a session into existence: worktree add, copying files, and the member's setup
// commands. A new branch starts from the branch of the same name on its remote,
// which it then tracks, when there is one, and else from what the session builds
// the member on. It does not fetch; callers put fetchStep ahead of the member
// steps. Rollback of the worktree step removes the worktree and any branch it
// created, and puts back one it moved.
func (c *Core) addMemberSteps(s *Session, mem manifest.Member, src string, plan branchPlan) []apply.Step {
	var steps []apply.Step
	dst := s.MemberDir(mem)
	branch := s.BranchFor(mem.Name)

	switch mem.Mode {
	case manifest.ModeActive:
		createdBranch, movedFrom := false, ""
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree add", Run: func(warn func(string)) error {
			if err := releaseStaleWorktrees(src, warn); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			pub := publicationOf(gitx.LiveRefs(src), src, branch, baseBranches(s, mem, src))
			if plan.exists {
				if err := gitx.WorktreeAddExisting(src, dst, branch); err != nil {
					return err
				}
				if !plan.forward || !pub.published() {
					return nil
				}
				var err error
				movedFrom, err = catchUp(src, dst, branch, pub, warn)
				return err
			}
			start := pub.ref
			if !pub.published() {
				base, err := baseOf(gitx.LiveRefs(src), s, mem, src, warn)
				if err != nil {
					return err
				}
				start = base.ref
			}
			if err := gitx.WorktreeAddBranch(src, dst, branch, start); err != nil {
				// git creates the branch before it checks the target path, so
				// a refused add (an occupied directory, say) leaves the branch
				// behind. It did not exist before this step, so it is ours to
				// remove.
				if gitx.BranchExists(src, branch) {
					_ = gitx.DeleteBranch(src, branch, true)
				}
				return err
			}
			createdBranch = true
			if pub.published() {
				return gitx.SetUpstream(src, branch, shortRef(pub.ref))
			}
			return nil
		}, Undo: func() error {
			if movedFrom != "" {
				if err := gitx.ResetKeep(dst, movedFrom); err != nil {
					return err
				}
			}
			return undoWorktree(src, dst, branch, createdBranch)
		}})
	case manifest.ModeReference:
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree add", Run: func(warn func(string)) error {
			if err := releaseStaleWorktrees(src, warn); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			ref, lost, err := refOf(s, mem, src, warn)
			if err != nil {
				return err
			}
			if lost != "" {
				warn(fmt.Sprintf("%s, which the session checks it out at, is gone; using %s", lost, displayRef(src, ref)))
			}
			return gitx.WorktreeAddDetached(src, dst, ref)
		}, Undo: func() error {
			return undoWorktree(src, dst, "", false)
		}})
	}

	if len(mem.Copy) > 0 {
		steps = append(steps, apply.Step{Member: mem.Name, Name: "copy files", Run: func(func(string)) error {
			_, err := copyGlobs(src, dst, mem.Copy)
			return err
		}})
	}
	steps = append(steps, c.memberHookSteps(s, mem, src, "setup", mem.Setup)...)
	return steps
}

// catchUp makes the branch checked out at wt track its remote copy, and moves it
// there when all it lacks is the copy's newer commits. It returns where the
// branch was when it moved. One that has diverged stays where it is, with a
// warning.
func catchUp(src, wt, branch string, pub publication, warn func(string)) (movedFrom string, err error) {
	if !pub.upstream {
		if err := gitx.SetUpstream(src, branch, shortRef(pub.ref)); err != nil {
			return "", err
		}
	}
	ahead, behind, err := gitx.AheadBehind(src, "refs/heads/"+branch, pub.ref)
	switch {
	case err != nil || behind == 0:
		return "", err
	case ahead > 0:
		warn(fmt.Sprintf("%s has diverged from %s; starting from the local branch", branch, shortRef(pub.ref)))
		return "", nil
	}
	tip, err := gitx.ResolveRef(wt, "HEAD")
	if err != nil {
		return "", err
	}
	return tip, gitx.ResetKeep(wt, pub.ref)
}

// undoWorktree reverses a worktree add: force-remove the worktree, prune
// the source, and delete the branch only if this run created it. The forced
// removal is safe here because the worktree was created moments ago by the
// same operation.
func undoWorktree(src, dst, branch string, deleteBranch bool) error {
	var errs []error
	if _, err := os.Stat(dst); err == nil {
		if err := gitx.WorktreeRemove(src, dst, true); err != nil {
			errs = append(errs, err)
		}
	}
	if err := gitx.WorktreePrune(src); err != nil {
		errs = append(errs, err)
	}
	if deleteBranch && branch != "" && gitx.BranchExists(src, branch) {
		if err := gitx.DeleteBranch(src, branch, true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
