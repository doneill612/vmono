package core

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/paths"
)

// InspectMember is one member's fully resolved model.
type InspectMember struct {
	Name   string `json:"name"`
	Mode   string `json:"mode"`
	Role   string `json:"role,omitempty"`
	Remote string `json:"remote,omitempty"`
	// Path is the absolute worktree directory; RelPath is relative to the
	// session root.
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`
	// Source is the absolute source repo.
	Source string `json:"source"`
	// GitCommonDir is the source repo's git directory. Commits made in the
	// worktree write objects and refs here, outside the package, so
	// sandboxing wrappers need to allow writes to it.
	GitCommonDir string `json:"git_common_dir,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Base         string `json:"base,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Exists       bool   `json:"exists"`
}

// InspectHarness is the resolved harness for a session.
type InspectHarness struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Adapter   string            `json:"adapter,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Path      string            `json:"path,omitempty"`
	Available bool              `json:"available"`
}

// InspectResult is the full resolved model of a session and its package.
type InspectResult struct {
	Package string `json:"package"`
	Session string `json:"session"`
	// Path is the session directory, which is where a harness is launched.
	Path string `json:"path"`
	// PackagePath is the package directory.
	PackagePath  string `json:"package_path"`
	ManifestPath string `json:"manifest_path"`
	SessionPath  string `json:"session_path"`
	// ShellGitDir is the session's own git directory.
	ShellGitDir string          `json:"shell_git_dir"`
	Branch      string          `json:"branch"`
	Harness     *InspectHarness `json:"harness,omitempty"`
	// LaunchEnv is the environment vmono sets when launching a harness.
	LaunchEnv map[string]string `json:"launch_env"`
	Members   []InspectMember   `json:"members"`
}

// Inspect resolves every path, each member's git common dir and branch, and
// the harness command, for scripts and wrapping tools. It reads only; a
// missing worktree is reported with Exists false rather than failing.
func (c *Core) Inspect(s *Session) (InspectResult, error) {
	p := s.Pkg
	out := InspectResult{
		Package: p.Name, Session: s.Name, Path: s.Dir, PackagePath: p.Dir,
		ManifestPath: manifest.Path(p.Dir), SessionPath: manifest.SessionPath(s.Dir),
		ShellGitDir: filepath.Join(s.Dir, ".git"), Branch: s.S.Branch,
		Members: []InspectMember{},
	}
	env := map[string]string{"VMONO_PACKAGE": p.Name, "VMONO_SESSION": s.Name, "VMONO_ROOT": s.Dir}
	if name, h, err := c.Cfg.ResolveHarness("", p.M.Harness); err == nil {
		ih := &InspectHarness{Name: name, Command: h.Command, Args: append([]string{}, h.Args...), Adapter: h.Adapter, Env: h.Env}
		if path, err := exec.LookPath(h.Command); err == nil {
			ih.Path, ih.Available = path, true
		}
		out.Harness = ih
		env["VMONO_AUTHOR"] = "agent:" + name
	}
	out.LaunchEnv = env

	for _, mem := range p.M.Members {
		src, err := paths.Abs(mem.Source)
		if err != nil {
			return out, err
		}
		im := InspectMember{
			Name: mem.Name, Mode: mem.Mode, Role: mem.Role, Remote: mem.Remote,
			Path: s.MemberDir(mem), RelPath: mem.Path, Source: src,
		}
		if mem.Mode == manifest.ModeActive {
			im.Branch, im.Base = s.BranchFor(mem.Name), mem.Base
		} else {
			im.Ref = s.S.RefFor(mem)
		}
		if _, err := os.Stat(im.Path); err == nil {
			im.Exists = true
			if cd, err := gitx.CommonDir(im.Path); err == nil {
				im.GitCommonDir = cd
			}
		}
		if im.GitCommonDir == "" {
			if cd, err := gitx.CommonDir(src); err == nil {
				im.GitCommonDir = cd
			}
		}
		out.Members = append(out.Members, im)
	}
	return out, nil
}
