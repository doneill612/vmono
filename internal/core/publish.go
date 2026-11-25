package core

import (
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
)

// memberBase is what a member's branch is built on and measured against.
type memberBase struct {
	// ref resolves to a commit in the member's source repo.
	ref string
	// manifest is the ref of the member's own base. It is ref, unless the
	// session builds the member on something else.
	manifest string
	// from is true when ref comes from the session rather than the member.
	from bool
	// lost names what the session built the member on when it can no longer be
	// found, in which case ref is the member's own base.
	lost string
}

// refs returns the distinct refs the member's work may have merged into.
func (b memberBase) refs() []string {
	if b.ref == b.manifest {
		return []string{b.ref}
	}
	return []string{b.manifest, b.ref}
}

// baseOf returns what the member's branch in the session is built on: the ref
// the session names for it, else the member's base. warn may be nil.
func baseOf(refs gitx.Refs, s *Session, mem manifest.Member, src string, warn func(string)) (memberBase, error) {
	own, err := resolveRefIn(refs, src, mem.Base, warn)
	if err != nil {
		return memberBase{}, err
	}
	b := memberBase{ref: own, manifest: own}
	from := s.S.Members[mem.Name].From
	if from == "" {
		return b, nil
	}
	if ref := resolveFrom(src, from); ref != "" {
		b.ref, b.from = ref, true
	} else {
		b.lost = from
	}
	return b, nil
}

// displayRef names ref for people. origin/HEAD is spelled as the branch it stands
// for.
func displayRef(src, ref string) string {
	if ref == "origin/HEAD" {
		if b := gitx.DefaultBranch(src); b != "" {
			return "origin/" + b
		}
	}
	return shortRef(ref)
}

// resolveFrom returns the ref that what a member is built on stands for now. A
// branch name is whichever of the local branch and its remote copy is further
// ahead. It is "" when nothing by that name exists.
func resolveFrom(src, from string) string {
	if gitx.BranchExists(src, from) {
		ref, _ := freshest(src, from)
		return ref
	}
	if ref := remoteCounterpart(src, from); ref != "" {
		return ref
	}
	if gitx.RefExists(src, from) {
		return from
	}
	return ""
}

// baseBranches returns the names of the branches a member's work is meant to
// merge into, which its own branch must never be pushed to: the one its base
// stands for and, when the session builds it on another, that one.
func baseBranches(s *Session, mem manifest.Member, src string) []string {
	var out []string
	if b := baseBranchOf(src, mem); b != "" {
		out = append(out, b)
	}
	if from := s.S.Members[mem.Name].From; from != "" {
		if remote, rest, ok := strings.Cut(from, "/"); ok && slices.Contains(gitx.Remotes(src), remote) {
			from = rest
		}
		out = append(out, from)
	}
	return out
}

// publication is where a member's branch is, or would be, on a remote.
type publication struct {
	// remote and dest name the branch on its remote. Both are empty when the
	// branch is not published.
	remote, dest string
	// ref is the remote-tracking ref that mirrors it, "" when it is not there.
	ref string
	// upstream is true when the local branch is configured to track it.
	upstream bool
}

// published reports whether the branch is on a remote as of the last fetch.
func (p publication) published() bool { return p.ref != "" }

// deleted reports whether the branch was on a remote and no longer is.
func (p publication) deleted() bool { return p.upstream && p.ref == "" }

// publicationOf returns where the local branch of src is published: the branch
// it tracks, else the branch of the same name on the primary remote. An
// upstream among bases does not count. Git sets one when it cuts a branch from
// origin/main in the ordinary way, and it says what the branch was cut from,
// not where it goes.
func publicationOf(refs gitx.Refs, src, branch string, bases []string) publication {
	if tr, ok := refs.UpstreamOf(branch); ok && !slices.Contains(bases, tr.Branch) {
		p := publication{remote: tr.Remote, dest: tr.Branch, upstream: true}
		if refs.Exists(tr.Ref) {
			p.ref = tr.Ref
		}
		return p
	}
	if slices.Contains(bases, branch) {
		return publication{}
	}
	if remote := primaryRemote(src); remote != "" {
		if ref := "refs/remotes/" + remote + "/" + branch; refs.Exists(ref) {
			return publication{remote: remote, dest: branch, ref: ref}
		}
	}
	return publication{}
}
