package core

import (
	"os"

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
	// Into is the branch an active member's work lands on when the session
	// is finished, and ToLand the commits of its own that are not there yet.
	Into   string `json:"into,omitempty"`
	ToLand int    `json:"to_land"`
	// IntoRemote is the remote branch Into is pushed to, such as "origin/main",
	// and IntoUnpushed the commits on Into that it lacks as of the last fetch.
	// Both are empty or zero when Into does not exist locally or has no remote
	// to push to.
	IntoRemote   string `json:"into_remote,omitempty"`
	IntoUnpushed int    `json:"into_unpushed"`
	// Rebasing is true while a rebase is stopped or waiting in the worktree.
	Rebasing bool   `json:"rebasing,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SessionStatus is the live state of one session of a package.
type SessionStatus struct {
	Package string `json:"package"`
	Session string `json:"session"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	// State is SessionFinished or SessionInProgress.
	State string `json:"state"`
	// FinishedAt is when the session was last finished, if it ever was.
	FinishedAt string         `json:"finished_at,omitempty"`
	Members    []MemberStatus `json:"members"`
}

// Session states. A session is finished when it was marked by Finish and
// still has nothing left to land and no uncommitted changes; any new commit
// or edit puts it back in progress without anyone recording that.
const (
	SessionInProgress = "in-progress"
	SessionFinished   = "finished"
)

// Status inspects every member's worktree in a session. A member whose
// directory is missing is reported as Missing rather than failing the whole
// call.
func (c *Core) Status(s *Session) SessionStatus {
	out := SessionStatus{Package: s.Pkg.Name, Session: s.Name, Path: s.Dir, Branch: s.S.Branch, Members: []MemberStatus{}}
	for _, mem := range s.Pkg.M.Members {
		out.Members = append(out.Members, memberStatus(s, mem))
	}
	out.State = stateOf(s, out.Members)
	if !s.S.Finished.IsZero() {
		out.FinishedAt = s.S.Finished.Format(timeFormat)
	}
	return out
}

// SessionState reports whether the session is finished. Only a session that
// was marked by Finish can be, so any other answers without touching git.
func (c *Core) SessionState(s *Session) string {
	if s.S.Finished.IsZero() {
		return SessionInProgress
	}
	return c.Status(s).State
}

// stateOf derives a session's state from its members' live status: finished
// when the session was marked and every active member is intact, clean, and
// has no commits of its own left to land.
func stateOf(s *Session, members []MemberStatus) string {
	if s.S.Finished.IsZero() {
		return SessionInProgress
	}
	for _, m := range members {
		if m.Mode != manifest.ModeActive {
			continue
		}
		if m.Missing || m.Error != "" || m.Rebasing || m.Dirty > 0 || m.Untracked > 0 || m.ToLand > 0 {
			return SessionInProgress
		}
	}
	return SessionFinished
}

// memberStatus gathers one member's state.
func memberStatus(s *Session, mem manifest.Member) MemberStatus {
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
	if mem.Mode == manifest.ModeActive {
		landingStatus(s, mem, dir, &ms)
	}
	return ms
}

// landingStatus fills the fields that say how far a member is from landed:
// the branch it lands on, how many of its commits are not there yet, and
// whether a rebase is under way. A member whose branch or refs cannot be
// resolved is left without them rather than failing the whole status.
func landingStatus(s *Session, mem manifest.Member, dir string, ms *MemberStatus) {
	ms.Rebasing = gitx.RebaseInProgress(dir)
	src, err := paths.Abs(mem.Source)
	if err != nil {
		return
	}
	into, _ := landingBranch(s, mem, src)
	if into == "" {
		return
	}
	ms.Into = into
	ms.IntoRemote, ms.IntoUnpushed = unpushedOn(src, into)
	base, err := resolveRef(src, mem.Base, nil)
	if err != nil {
		return
	}
	target, _ := landingTarget(src, into, base)
	if n, err := ownCommits(src, dir, into, base, target); err == nil {
		ms.ToLand = n
	}
}
