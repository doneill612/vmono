package core

import (
	"slices"
	"time"

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

// BranchChoice is one branch a member's new branch could be built on, or a
// reference member checked out at: one of the source repo's own, or one on its
// remote.
type BranchChoice struct {
	// Name is how people write it: main for the source's own branch, and
	// origin/main for the one on its remote.
	Name string
	// Branch is its name without the remote's: main for both.
	Branch string
	// Ref names exactly this one, refs/heads/main or refs/remotes/origin/main,
	// which is what to give a session to have it followed and no other.
	Ref string
	// Remote is true for a branch on the remote.
	Remote bool
	// When is the time of its latest commit.
	When time.Time
	// Counterpart is, for a local branch, the branch of the same name on the
	// remote, "" when there is none. Ahead counts the commits the local one has
	// that the counterpart lacks, and Behind the reverse.
	Counterpart   string
	Ahead, Behind int
}

// MemberBranches is one member's branches.
type MemberBranches struct {
	Member string
	// Branches holds the source's own branches and then those on its remote.
	Branches []BranchChoice
	// FetchErr is why the remote could not be fetched; Branches then holds
	// what the last fetch that worked saw.
	FetchErr error
	// ListErr is why the branches could not be listed at all.
	ListErr error
}

// ListBranches lists the branches a session of p could build on, for each active
// member, or could check out, for the one member named: the local ones, and
// those on the remote as of the last fetch, or after fetching when fetch is
// true. A member that cannot be fetched or listed is reported rather than
// failing the rest.
func (c *Core) ListBranches(p *Package, member string, fetch bool) []MemberBranches {
	var out []MemberBranches
	var srcs []string
	for _, mem := range p.M.Members {
		if (member == "" && mem.Mode != manifest.ModeActive) || (member != "" && mem.Name != member) {
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
		if out[i].ListErr == nil {
			out[i].Branches, out[i].ListErr = branchChoices(src)
		}
	}
	return out
}

// branchChoices lists the branches of src and of its primary remote, counting
// how far apart each local one is from the remote's of the same name. A pair
// that cannot be compared is listed without a count.
func branchChoices(src string) ([]BranchChoice, error) {
	local, err := gitx.LocalBranches(src)
	if err != nil {
		return nil, err
	}
	var remote []gitx.ListedBranch
	name := primaryRemote(src)
	if name != "" {
		if remote, err = gitx.RemoteBranches(src, name); err != nil {
			return nil, err
		}
	}
	tips := make(map[string]string, len(remote))
	for _, b := range remote {
		tips[b.Name] = b.Tip
	}
	var out []BranchChoice
	for _, b := range local {
		ch := BranchChoice{Name: b.Name, Branch: b.Name, Ref: localRefs + b.Name, When: b.When}
		if tip, ok := tips[b.Name]; ok {
			ch.Counterpart = name + "/" + b.Name
			if tip != b.Tip {
				if ahead, behind, err := gitx.AheadBehind(src, ch.Ref, "refs/remotes/"+ch.Counterpart); err == nil {
					ch.Ahead, ch.Behind = ahead, behind
				}
			}
		}
		out = append(out, ch)
	}
	for _, b := range remote {
		full := name + "/" + b.Name
		out = append(out, BranchChoice{Name: full, Branch: b.Name, Ref: "refs/remotes/" + full, Remote: true, When: b.When})
	}
	return out, nil
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
