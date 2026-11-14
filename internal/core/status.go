package core

import (
	"os"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
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
	Error     string `json:"error,omitempty"`
}

// PackageStatus is the live state of a whole package.
type PackageStatus struct {
	Name    string         `json:"name"`
	Path    string         `json:"path"`
	Branch  string         `json:"branch"`
	Members []MemberStatus `json:"members"`
}

// Status inspects every member's worktree. A member whose directory is
// missing is reported as Missing rather than failing the whole call.
func (c *Core) Status(p *Package) PackageStatus {
	out := PackageStatus{Name: p.Name, Path: p.Dir, Branch: p.M.Branch, Members: []MemberStatus{}}
	for _, mem := range p.M.Members {
		out.Members = append(out.Members, memberStatus(p, mem))
	}
	return out
}

// memberStatus gathers one member's state.
func memberStatus(p *Package, mem manifest.Member) MemberStatus {
	ms := MemberStatus{Name: mem.Name, Mode: mem.Mode, Path: mem.Path, Role: mem.Role}
	dir := p.MemberDir(mem)
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
