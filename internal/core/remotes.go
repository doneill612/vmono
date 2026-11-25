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

// remoteCounterpart returns the remote-tracking ref that stands for the local
// branch of src: the upstream it tracks, else the branch of the same name on the
// primary remote. It is "" when there is none on disk as of the last fetch.
func remoteCounterpart(src, branch string) string {
	refs := gitx.LiveRefs(src)
	if tr, ok := refs.UpstreamOf(branch); ok {
		if refs.Exists(tr.Ref) {
			return tr.Ref
		}
		return ""
	}
	if remote := primaryRemote(src); remote != "" {
		if ref := "refs/remotes/" + remote + "/" + branch; refs.Exists(ref) {
			return ref
		}
	}
	return ""
}

// freshest returns whichever of the existing local branch and its remote
// counterpart contains the other, preferring the local one when they are
// equal. When each has commits the other lacks, it returns the local branch
// and diverged is true.
func freshest(src, branch string) (ref string, diverged bool) {
	local := "refs/heads/" + branch
	remote := remoteCounterpart(src, branch)
	switch {
	case remote == "" || gitx.IsMerged(src, remote, local):
		return local, false
	case gitx.IsMerged(src, local, remote):
		return remote, false
	}
	return local, true
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

// RemoteBranches lists the remote branches a session of p could work on or build on,
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
