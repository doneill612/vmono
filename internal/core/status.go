package core

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// MemberStatus is the live git state of one member.
type MemberStatus struct {
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	Path      string `json:"path"`
	Role      string `json:"role,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Head      string `json:"head,omitempty"`
	Upstream  string `json:"upstream,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Dirty     int    `json:"dirty"`
	Untracked int    `json:"untracked"`
	Ahead     int    `json:"ahead"`
	Behind    int    `json:"behind"`
	// BuiltOn is what an active member's branch is built on and measured
	// against, such as "origin/main", and OwnCommits the commits it has that
	// its base lacks.
	BuiltOn    string `json:"built_on,omitempty"`
	OwnCommits int    `json:"own_commits"`
	// Publish says how far an active member's branch is from published and
	// merged, as of the last fetch: one of the Publish constants. It is empty
	// when that cannot be told.
	Publish string `json:"publish,omitempty"`
	// RemoteBranch is where the branch is published, or was, such as
	// "origin/token-refresh".
	RemoteBranch string `json:"remote_branch,omitempty"`
	// Unpushed counts the branch's commits its remote copy lacks, and Unpulled
	// the remote copy's it lacks itself.
	Unpushed int `json:"unpushed"`
	Unpulled int `json:"unpulled"`
	// MergedBy is how the branch was merged into MergedInto, set when Publish
	// is PublishMerged: one of the Merged constants.
	MergedBy   string `json:"merged_by,omitempty"`
	MergedInto string `json:"merged_into,omitempty"`
	// Rebasing is true while a rebase is stopped or waiting in the worktree.
	Rebasing bool   `json:"rebasing,omitempty"`
	Error    string `json:"error,omitempty"`
}

// How far a member's branch is from published and merged.
const (
	// PublishEmpty: it has no commits of its own and is on no remote.
	PublishEmpty = "empty"
	// PublishLocal: it has commits, and is on no remote.
	PublishLocal = "unpublished"
	// PublishUnpushed: its remote copy lacks some of its commits.
	PublishUnpushed = "unpushed"
	// PublishPushed: its remote copy is the same.
	PublishPushed = "pushed"
	// PublishBehind: its remote copy has commits it lacks, and no others differ.
	PublishBehind = "behind"
	// PublishDiverged: it and its remote copy each have commits the other lacks.
	PublishDiverged = "diverged"
	// PublishMerged: its work has been merged into what it is built on.
	PublishMerged = "merged"
	// PublishDeleted: its remote copy was deleted, and its work is not
	// recognizably merged. A pull request that was merged with changes, or
	// before the branch's latest commits, looks like this.
	PublishDeleted = "remote-deleted"
)

// PublishText words how far an active member's branch is from published and
// merged, for people. It is empty when that cannot be told.
func (m MemberStatus) PublishText() string {
	commits := func(n int) string {
		if n == 1 {
			return "1 commit"
		}
		return fmt.Sprintf("%d commits", n)
	}
	switch m.Publish {
	case PublishEmpty:
		return "no commits yet"
	case PublishLocal:
		return commits(m.OwnCommits) + ", not pushed"
	case PublishUnpushed:
		return fmt.Sprintf("%s to push to %s", commits(m.Unpushed), m.RemoteBranch)
	case PublishPushed:
		return "pushed to " + m.RemoteBranch
	case PublishBehind:
		return fmt.Sprintf("%s behind %s", commits(m.Unpulled), m.RemoteBranch)
	case PublishDiverged:
		return fmt.Sprintf("diverged from %s (%d ahead, %d behind)", m.RemoteBranch, m.Unpushed, m.Unpulled)
	case PublishMerged:
		return fmt.Sprintf("merged (%s) into %s", m.MergedBy, m.MergedInto)
	case PublishDeleted:
		return m.RemoteBranch + " was deleted, its PR probably merged"
	}
	return ""
}

// Session states. They are derived from the members each time and stored
// nowhere.
const (
	// SessionInProgress: there is work that is uncommitted or not pushed, or
	// nothing has been done yet.
	SessionInProgress = "in-progress"
	// SessionPushed: everything done is on its remotes.
	SessionPushed = "pushed"
	// SessionMerged: everything done has been merged, so the session is done
	// with.
	SessionMerged = "merged"
)

// SessionProgress is how far a session's work is from published and merged, as
// of the last fetch.
type SessionProgress struct {
	// State is SessionInProgress, SessionPushed or SessionMerged.
	State string `json:"state,omitempty"`
	// MergedBy lists how members' branches were merged, one of each kind.
	MergedBy []string `json:"merged_by,omitempty"`
	// RemoteDeleted lists the members whose remote branch was deleted without
	// their work being recognizably merged.
	RemoteDeleted []string `json:"remote_deleted,omitempty"`
}

// Label words the state for people, such as "merged (squash)".
func (p SessionProgress) Label() string {
	label := p.State
	if p.State == SessionMerged && len(p.MergedBy) > 0 {
		label += " (" + strings.Join(p.MergedBy, ", ") + ")"
	}
	if len(p.RemoteDeleted) > 0 {
		label += ", remote branch deleted"
	}
	return label
}

// SessionStatus is the live state of one session of a package.
type SessionStatus struct {
	Package string `json:"package"`
	Session string `json:"session"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	SessionProgress
	Members []MemberStatus `json:"members"`
}

// Dirty reports whether any member has uncommitted or untracked files.
func (s SessionStatus) Dirty() bool {
	return slices.ContainsFunc(s.Members, func(m MemberStatus) bool { return m.Dirty > 0 || m.Untracked > 0 })
}

// Status inspects every member's worktree in a session. A member whose
// directory is missing is reported as Missing rather than failing the whole
// call.
func (c *Core) Status(s *Session) SessionStatus {
	out := SessionStatus{Package: s.Pkg.Name, Session: s.Name, Path: s.Dir, Branch: s.S.Branch, Members: []MemberStatus{}}
	for _, mem := range s.Pkg.M.Members {
		out.Members = append(out.Members, memberStatus(s, mem))
	}
	out.SessionProgress = progressOf(out.Members)
	return out
}

// progressOf derives a session's progress from its members' live status.
// Members with nothing on them do not count either way.
func progressOf(members []MemberStatus) SessionProgress {
	var out SessionProgress
	open, pushed, merged := false, false, false
	for _, m := range members {
		if m.Mode != manifest.ModeActive {
			continue
		}
		if m.Missing || m.Error != "" || m.Rebasing || m.Dirty > 0 || m.Untracked > 0 {
			open = true
		}
		switch m.Publish {
		case PublishEmpty:
		case PublishMerged:
			merged = true
			if !slices.Contains(out.MergedBy, m.MergedBy) {
				out.MergedBy = append(out.MergedBy, m.MergedBy)
			}
		case PublishPushed, PublishBehind:
			pushed = true
		case PublishDeleted:
			open = true
			out.RemoteDeleted = append(out.RemoteDeleted, m.Name)
		default:
			open = true
		}
	}
	switch {
	case open || !pushed && !merged:
		out.State = SessionInProgress
	case pushed:
		out.State = SessionPushed
	default:
		out.State = SessionMerged
	}
	return out
}

// memberStatus gathers one member's state.
func memberStatus(s *Session, mem manifest.Member) MemberStatus {
	ms := worktreeStatus(s, mem)
	if mem.Mode == manifest.ModeActive && !ms.Missing && ms.Error == "" {
		publishStatus(s, mem, &ms)
	}
	return ms
}

// worktreeStatus gathers the state of one member's worktree alone.
func worktreeStatus(s *Session, mem manifest.Member) MemberStatus {
	ms := MemberStatus{Name: mem.Name, Mode: mem.Mode, Path: mem.Path, Role: mem.Role}
	dir := s.MemberDir(mem)
	if _, err := os.Stat(dir); err != nil {
		ms.Missing = true
		return ms
	}
	st, err := gitx.StatusOf(dir)
	if err != nil {
		ms.Error = err.Error()
		return ms
	}
	ms.Branch, ms.Head, ms.Upstream = st.Branch, st.Head, st.Upstream
	ms.Dirty, ms.Untracked, ms.Ahead, ms.Behind = st.Dirty, st.Untracked, st.Ahead, st.Behind
	if st.Detached {
		ms.Ref = mem.Ref
		if ms.Ref == "" {
			ms.Ref = st.Head
		}
	}
	return ms
}

// publishStatus fills the fields that say how far a member's branch is from
// published and merged, and whether a rebase is under way. A member whose
// branch or refs cannot be resolved is left without them rather than failing
// the whole status.
func publishStatus(s *Session, mem manifest.Member, ms *MemberStatus) {
	ms.Rebasing = gitx.RebaseInProgress(s.MemberDir(mem))
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return
	}
	branch := s.BranchFor(mem.Name)
	refs := gitx.SnapshotRefs(src, branch)
	if !refs.BranchExists(branch) {
		return
	}
	base, err := baseOf(refs, s, mem, src, nil)
	if err != nil {
		return
	}
	local := "refs/heads/" + branch
	own, err := gitx.RevCount(src, append([]string{local, "--not"}, base.refs()...)...)
	if err != nil {
		return
	}
	ms.BuiltOn, ms.OwnCommits = displayRef(src, base.ref), own
	pub := publicationOf(refs, src, branch, baseBranches(s, mem, src))
	if pub.remote != "" {
		ms.RemoteBranch = pub.remote + "/" + pub.dest
		if how, into := mergedIntoAny(src, local, base.refs()); how != "" {
			ms.Publish, ms.MergedBy, ms.MergedInto = PublishMerged, how, displayRef(src, into)
			return
		}
	}
	switch {
	case pub.deleted():
		ms.Publish = PublishDeleted
	case !pub.published() && own == 0:
		ms.Publish = PublishEmpty
	case !pub.published():
		ms.Publish, ms.Unpushed = PublishLocal, own
	default:
		ahead, behind, err := gitx.AheadBehind(src, local, pub.ref)
		if err != nil {
			return
		}
		ms.Unpushed, ms.Unpulled = ahead, behind
		switch {
		case ahead > 0 && behind > 0:
			ms.Publish = PublishDiverged
		case ahead > 0:
			ms.Publish = PublishUnpushed
		case behind > 0:
			ms.Publish = PublishBehind
		default:
			ms.Publish = PublishPushed
		}
	}
}
