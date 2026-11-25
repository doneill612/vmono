package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doneill612/vmono/internal/apply"
	"github.com/doneill612/vmono/internal/config"
	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
	"github.com/doneill612/vmono/internal/render"
)

// SessionOptions tunes CreateSession.
type SessionOptions struct {
	// Branch overrides the session's default branch,
	// <branch_prefix><package>/<session>.
	Branch string
	// ReuseBranch checks out a session branch that already exists in a
	// source repo instead of failing. The CLI requires --reuse-branch; the
	// TUI asks and retries on BranchExistsError.
	ReuseBranch bool
	// Into sets the landing branch of every active member. IntoByMember sets
	// individual members' and wins over Into. A member given neither lands on
	// the branch its source repo has checked out when the session is created
	// (see DefaultLanding). The choices are recorded in the session file.
	//
	// A landing branch other than the one the member's base names is also the
	// session's starting point when it exists in the source, so a session can
	// build on a feature branch and land back on it. When it does not exist it
	// is created at finish.
	Into         string
	IntoByMember map[string]string
	// Track names a branch on the members' remotes to start from and land back
	// on: the landing branch of every active member whose remote has it. A
	// member whose remote lacks it keeps its default. TrackByMember names one
	// for individual members, wins over Track, and fails for a member whose
	// remote lacks it. Tracking fetches first, whatever fetch_on_create says,
	// since the branch may be one the source has never seen. No local branch
	// is made until finish.
	Track         string
	TrackByMember map[string]string
}

// tracking reports whether the options start any member from a remote branch.
func (o SessionOptions) tracking() bool { return o.Track != "" || len(o.TrackByMember) > 0 }

// resolveTrack turns the tracked branches in opts into landing branches, as of
// the last fetch, and returns the options with IntoByMember filled in. It
// fails when a member named in TrackByMember has no such branch on its remote,
// when no member has the one named for all of them, and when a member is given
// both a tracked and a landing branch.
func resolveTrack(p *Package, opts SessionOptions, srcs map[string]string, warn func(member, msg string)) (SessionOptions, error) {
	if opts.Track != "" && opts.Into != "" {
		return opts, errors.New("--track and --into cannot both name a branch for every member")
	}
	into := map[string]string{}
	for member, b := range opts.IntoByMember {
		if opts.TrackByMember[member] != "" {
			return opts, fmt.Errorf("--track and --into both name a branch for %s", member)
		}
		into[member] = b
	}
	active := map[string]bool{}
	found := 0
	type miss struct{ member, msg string }
	var missing []miss
	for _, mem := range p.M.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		active[mem.Name] = true
		branch, explicit := opts.TrackByMember[mem.Name], true
		if branch == "" {
			branch, explicit = opts.Track, false
		}
		if branch == "" || (!explicit && into[mem.Name] != "") {
			continue
		}
		remote := primaryRemote(srcs[mem.Name])
		if remote != "" && branch != "HEAD" && gitx.RefExists(srcs[mem.Name], "refs/remotes/"+remote+"/"+branch) {
			into[mem.Name] = branch
			found++
			continue
		}
		msg := fmt.Sprintf("no branch %q on %s", branch, firstNonEmpty(remote, "any remote"))
		if explicit {
			return opts, NotFound("%s: %s", mem.Name, msg)
		}
		missing = append(missing, miss{mem.Name, msg + "; starting from its default"})
	}
	for member := range opts.TrackByMember {
		if !active[member] {
			return opts, fmt.Errorf("--track names %q, which is not an active member of %s", member, p.Name)
		}
	}
	if opts.Track != "" && found == 0 {
		return opts, NotFound("no member of %s has a branch %q on its remote", p.Name, opts.Track)
	}
	for _, m := range missing {
		warn(m.member, m.msg)
	}
	opts.IntoByMember = into
	return opts, nil
}

// DefaultLanding returns, for each active member of p, the branch its source
// repo has checked out: where the user was working, and so where the work
// most likely belongs. A source on a detached HEAD has no entry, and its
// member lands on the branch its base names instead.
func (c *Core) DefaultLanding(p *Package) map[string]string {
	out := map[string]string{}
	for _, mem := range p.M.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		src, err := paths.Abs(mem.Source)
		if err != nil {
			continue
		}
		if br := gitx.CurrentBranch(src); br != "" {
			out[mem.Name] = br
		}
	}
	return out
}

// landingFor builds the landing branches recorded in a new session: the
// checked-out branch of each active member's source, replaced by opts.Into
// for all members when given, and by opts.IntoByMember for the members it
// names. A checked-out branch that is the session's own branch is not a
// landing branch and is left out; an explicit one is kept, for validation to
// refuse. It rejects a member that is not an active
// member of the package.
func (c *Core) landingFor(p *Package, branch string, opts SessionOptions) (map[string]string, error) {
	into := c.DefaultLanding(p)
	for name, b := range into {
		if b == branch {
			delete(into, name)
		}
	}
	active := map[string]bool{}
	for _, mem := range p.M.Members {
		if mem.Mode == manifest.ModeActive {
			active[mem.Name] = true
			if opts.Into != "" {
				into[mem.Name] = opts.Into
			}
		}
	}
	for name, b := range opts.IntoByMember {
		if !active[name] {
			return nil, fmt.Errorf("--into names %q, which is not an active member of %s", name, p.Name)
		}
		into[name] = b
	}
	if len(into) == 0 {
		return nil, nil
	}
	return into, nil
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
// elsewhere and does not collide with another branch's ref path, and base/ref
// resolve. srcs maps every
// member of m (not only check) to its absolute source. It returns the
// members whose branch already exists in their source.
func preflightMembers(m *manifest.Manifest, branchFor func(member string) string, srcs map[string]string, check []manifest.Member, reuse bool, removing map[string]bool) (existing map[string]bool, err error) {
	existing = map[string]bool{}
	if err := checkDistinctSources(m, srcs); err != nil {
		return nil, err
	}
	for _, mem := range check {
		src := srcs[mem.Name]
		switch mem.Mode {
		case manifest.ModeActive:
			branch := branchFor(mem.Name)
			if at, err := gitx.BranchCheckedOutAt(src, branch); err != nil {
				return nil, fmt.Errorf("%s: %w", mem.Name, err)
			} else if at != "" && !removing[realPath(at)] {
				return nil, fmt.Errorf("%s: branch %q is already checked out at %s", mem.Name, branch, at)
			}
			if gitx.BranchExists(src, branch) {
				if !reuse {
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

// preflightInto checks each member's recorded landing branch against its
// source before a session is created, or its landing branches change, so a
// finish never fails late on a branch git could not hold. The name must be a
// valid branch name, and a branch that does not exist yet must not collide
// with another branch's ref path, since finish creates it. Members with no
// recorded landing branch are skipped.
func preflightInto(ms *manifest.Session, members []manifest.Member, srcs map[string]string) error {
	for _, mem := range members {
		into := ms.Into[mem.Name]
		if mem.Mode != manifest.ModeActive || into == "" {
			continue
		}
		src := srcs[mem.Name]
		if !gitx.ValidBranchName(src, into) {
			return fmt.Errorf("%s: %q is not a valid branch name", mem.Name, into)
		}
		if gitx.BranchExists(src, into) {
			continue
		}
		if other, clash := gitx.BranchRefConflict(src, into); clash {
			return fmt.Errorf("%s: landing branch %q conflicts with existing branch %q (git cannot hold both); choose a different --into", mem.Name, into, other)
		}
	}
	return nil
}

// cutPoint returns the ref a new session branch for mem is cut from. When the
// member's recorded landing branch is not the branch its base names, and it
// exists in the source as a local branch or on its remote, the session starts
// from it: the work is meant to build on that branch and land back on it. When
// it exists in both places the one further ahead is used, and the local one,
// with a warning, when they have diverged. Otherwise it starts from the member's base. The branch the base names is
// deliberately not used: the local copy can lag behind origin/main, and the
// manifest's base already says where ordinary work starts.
func cutPoint(src string, ms *manifest.Session, mem manifest.Member, warn func(string)) (string, error) {
	if into := ms.Into[mem.Name]; into != "" && into != baseBranchOf(src, mem) {
		if gitx.BranchExists(src, into) {
			ref, diverged := freshest(src, into)
			if diverged && warn != nil {
				warn(fmt.Sprintf("%s has diverged from %s; starting from the local branch", into, shortRef(remoteCounterpart(src, into))))
			}
			return ref, nil
		}
		if ref := remoteCounterpart(src, into); ref != "" {
			return ref, nil
		}
	}
	return resolveRef(src, mem.Base, warn)
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
			_, err := gitx.CommitAll(dir, "vmono: create "+m.Name, "")
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
	if err := manifest.ValidateName(name); err != nil {
		return nil, err
	}
	ms := &manifest.Session{Package: p.Name, Name: name, Branch: opts.Branch, Created: time.Now().Truncate(time.Second)}
	ms.ApplyDefaults(c.Cfg.BranchPrefix)
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
	fetch := c.fetchStep(srcList...)
	if opts.tracking() {
		// The landing branches depend on what the remotes have, so this fetch
		// has to happen ahead of the checks, and replaces the usual one.
		fetch = nil
		if err := (&apply.Runner{Events: c.Events}).Run([]apply.Step{{Name: "fetch", Run: func(warn func(string)) error {
			fetchSources(srcList, warn)
			return nil
		}}}); err != nil {
			return nil, err
		}
		if opts, err = resolveTrack(p, opts, srcs, func(member, msg string) { c.warnEvent(member, "track", msg) }); err != nil {
			return nil, err
		}
	}
	into, err := c.landingFor(p, ms.Branch, opts)
	if err != nil {
		return nil, err
	}
	ms.Into = into
	if err := ms.Validate(); err != nil {
		return nil, err
	}
	existing, err := preflightMembers(p.M, ms.BranchFor, srcs, p.M.Members, opts.ReuseBranch, nil)
	if err != nil {
		return nil, err
	}
	if err := preflightInto(ms, p.M.Members, srcs); err != nil {
		return nil, err
	}

	s := &Session{Pkg: p, Name: name, Dir: dir, S: ms}
	steps := append(fetch, apply.Step{
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
	})
	for _, mem := range p.M.Members {
		steps = append(steps, c.addMemberSteps(s, mem, srcs[mem.Name], existing[mem.Name])...)
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

// addMemberSteps returns the apply steps that bring one member's worktree in
// a session into existence: worktree add, copying files, and the member's setup
// commands. It does not fetch; callers
// put fetchStep ahead of the member steps. Rollback of the
// worktree step removes the worktree and any branch it created.
func (c *Core) addMemberSteps(s *Session, mem manifest.Member, src string, branchExists bool) []apply.Step {
	var steps []apply.Step
	dst := s.MemberDir(mem)
	branch := s.BranchFor(mem.Name)

	switch mem.Mode {
	case manifest.ModeActive:
		createdBranch := false
		steps = append(steps, apply.Step{Member: mem.Name, Name: "worktree add", Run: func(warn func(string)) error {
			if err := releaseStaleWorktrees(src, warn); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if branchExists {
				return gitx.WorktreeAddExisting(src, dst, branch)
			}
			base, err := cutPoint(src, s.S, mem, warn)
			if err != nil {
				return err
			}
			if err := gitx.WorktreeAddBranch(src, dst, branch, base); err != nil {
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
			return nil
		}, Undo: func() error {
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
			ref, err := resolveRef(src, mem.Ref, warn)
			if err != nil {
				return err
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
