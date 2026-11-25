package gitx

import "strings"

// Refs answers questions about a repo's refs. One from LiveRefs asks git each
// time. One from SnapshotRefs has read the refs around a single branch with
// one git command and answers from them, asking git only about refs outside
// the snapshot. The answers are the same as long as nothing changes the refs
// in between, so a snapshot is for one read-only look at a repo: status, not
// anything that moves branches as it goes.
type Refs struct {
	repo   string
	branch string
	// known maps the full name of each ref in the snapshot to the branch it
	// tracks. It is nil for live refs.
	known map[string]tracked
}

// tracked is a ref's upstream as git reports it, both parts empty for none.
type tracked struct{ ref, remote string }

// LiveRefs returns a Refs that always asks git.
func LiveRefs(repo string) Refs { return Refs{repo: repo} }

// SnapshotRefs reads the local branch, the branch of the same name on every
// remote, and every remote's HEAD. It falls back to live refs when they can't
// be read.
func SnapshotRefs(repo, branch string) Refs {
	live := LiveRefs(repo)
	if branch == "" {
		return live
	}
	out, err := run(repo, "for-each-ref", "--format=%(refname)\t%(upstream)\t%(upstream:remotename)",
		"refs/heads/"+branch, "refs/remotes/*/"+branch, "refs/remotes/*/HEAD")
	if err != nil {
		return live
	}
	r := Refs{repo: repo, branch: branch, known: map[string]tracked{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) == 3 {
			r.known[f[0]] = tracked{ref: f[1], remote: strings.TrimSpace(f[2])}
		}
	}
	return r
}

// covers reports whether the snapshot would hold the ref if it existed. The
// patterns it was read with don't match a remote whose name has a slash.
func (r Refs) covers(ref string) bool {
	if r.known == nil {
		return false
	}
	if ref == "refs/heads/"+r.branch {
		return true
	}
	rest, ok := strings.CutPrefix(ref, "refs/remotes/")
	if !ok {
		return false
	}
	_, name, ok := strings.Cut(rest, "/")
	return ok && (name == r.branch || name == "HEAD")
}

// Exists reports whether ref, in any form git understands, resolves to a
// commit.
func (r Refs) Exists(ref string) bool {
	if r.covers(ref) {
		_, ok := r.known[ref]
		return ok
	}
	if _, ok := r.known["refs/remotes/"+ref]; ok {
		return true
	}
	return RefExists(r.repo, ref)
}

// BranchExists reports whether a local branch exists.
func (r Refs) BranchExists(branch string) bool {
	if ref := "refs/heads/" + branch; r.covers(ref) {
		_, ok := r.known[ref]
		return ok
	}
	return BranchExists(r.repo, branch)
}

// UpstreamOf is like the function of the same name.
func (r Refs) UpstreamOf(branch string) (Tracking, bool) {
	ref := "refs/heads/" + branch
	if !r.covers(ref) {
		return UpstreamOf(r.repo, branch)
	}
	return newTracking(r.known[ref])
}

// newTracking turns a ref's upstream into a Tracking. It is false when there
// is none, or when it is another local branch.
func newTracking(t tracked) (Tracking, bool) {
	prefix := "refs/remotes/" + t.remote + "/"
	if t.ref == "" || t.remote == "" || t.remote == "." || !strings.HasPrefix(t.ref, prefix) {
		return Tracking{}, false
	}
	return Tracking{Remote: t.remote, Branch: strings.TrimPrefix(t.ref, prefix), Ref: t.ref}, true
}
