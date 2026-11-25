package core

import (
	"slices"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// primaryRemote returns the remote a branch of src with no upstream belongs
// to: origin, or the repo's only remote. It is "" for a repo with no remote,
// or with several and no origin, where there is nothing to choose by.
func primaryRemote(src string) string {
	remotes := gitx.Remotes(src)
	switch {
	case slices.Contains(remotes, "origin"):
		return "origin"
	case len(remotes) == 1:
		return remotes[0]
	}
	return ""
}

// remoteCounterpart returns the remote-tracking ref that stands for the
// landing branch into of src: the upstream the local branch tracks, else the
// branch of the same name on the primary remote. It is "" when there is none
// on disk as of the last fetch.
func remoteCounterpart(src, into string) string {
	return remoteCounterpartIn(gitx.LiveRefs(src), src, into)
}

// remoteCounterpartIn is remoteCounterpart answered from refs.
func remoteCounterpartIn(refs gitx.Refs, src, into string) string {
	if tr, ok := refs.UpstreamOf(into); ok {
		if refs.Exists(tr.Ref) {
			return tr.Ref
		}
		return ""
	}
	if remote := primaryRemote(src); remote != "" {
		if ref := "refs/remotes/" + remote + "/" + into; refs.Exists(ref) {
			return ref
		}
	}
	return ""
}

// freshest returns whichever of the existing local branch into and its remote
// counterpart contains the other, preferring the local one when they are
// equal. When each has commits the other lacks, it returns the local branch
// and diverged is true.
func freshest(src, into string) (ref string, diverged bool) {
	local := "refs/heads/" + into
	remote := remoteCounterpart(src, into)
	switch {
	case remote == "" || gitx.IsMerged(src, remote, local):
		return local, false
	case gitx.IsMerged(src, local, remote):
		return remote, false
	}
	return local, true
}

// landingTarget returns the ref a member's work is measured and rebased
// against, and whether the landing branch exists as a local branch. A branch
// that exists only on its remote stands in for it, and one that exists nowhere
// is measured against the member's base, since finish will create it there.
func landingTarget(refs gitx.Refs, src, into, base string) (target string, exists bool) {
	if refs.BranchExists(into) {
		return "refs/heads/" + into, true
	}
	if ref := remoteCounterpartIn(refs, src, into); ref != "" {
		return ref, false
	}
	return base, false
}

// ownCommits counts the commits in the worktree that are reachable from HEAD
// but from none of the member's base, its landing target, and the remote copy
// of its landing branch into: the session's own work still to land. The remote
// copy matters for a session updated onto it while the local branch lags, or
// merged with it: what it picked up there is not its own.
func ownCommits(refs gitx.Refs, src, wt, into, base, target string) (int, error) {
	not := []string{"HEAD", "--not", base, target}
	if ref := remoteCounterpartIn(refs, src, into); ref != "" {
		not = append(not, ref)
	}
	return gitx.RevCount(wt, not...)
}

// replaysUpstream reports whether rebasing the commits after fork onto target
// would copy commits that belong to the remote copy of the landing branch.
// That happens when a session holding them is rebased onto a local branch that
// lacks them, and the copies would make the local branch diverge for good.
func replaysUpstream(src, wt, into, fork, target string) bool {
	ref := remoteCounterpart(src, into)
	if ref == "" {
		return false
	}
	all, err := gitx.RevCount(wt, "HEAD", "--not", fork, target)
	if err != nil {
		return false
	}
	own, err := gitx.RevCount(wt, "HEAD", "--not", fork, target, ref)
	return err == nil && own < all
}

// MemberBranches is one active member's branches on its remote.
type MemberBranches struct {
	Member   string
	Branches []gitx.RemoteBranch
	// FetchErr is why the remote could not be fetched; Branches then holds
	// what the last fetch that worked saw.
	FetchErr error
	// ListErr is why the branches could not be listed at all.
	ListErr error
}

// RemoteBranches lists the remote branches a session of p could start from,
// for each active member: as of the last fetch, or after fetching when fetch
// is true. A member that cannot be fetched or listed is reported rather than
// failing the rest.
func (c *Core) RemoteBranches(p *Package, fetch bool) []MemberBranches {
	var out []MemberBranches
	var srcs []string
	for _, mem := range p.M.Members {
		if mem.Mode != manifest.ModeActive {
			continue
		}
		src, err := paths.Abs(mem.Source)
		out, srcs = append(out, MemberBranches{Member: mem.Name, ListErr: err}), append(srcs, src)
	}
	failed := map[string]error{}
	if fetch {
		for _, r := range gitx.FetchMany(srcs, gitx.DefaultFetchConcurrency) {
			for _, path := range r.Paths {
				failed[path] = r.Err
			}
		}
	}
	for i, src := range srcs {
		out[i].FetchErr = failed[src]
		if out[i].ListErr != nil {
			continue
		}
		if remote := primaryRemote(src); remote != "" {
			out[i].Branches, out[i].ListErr = gitx.RemoteBranches(src, remote)
		} else if _, err := gitx.CommonDir(src); err != nil {
			out[i].ListErr = err
		}
	}
	return out
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
