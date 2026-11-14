// Package discover finds source repos: those under configured source roots,
// those listed in config, and those added by path before.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/index"
)

// Repo is a known source repo.
type Repo struct {
	// Name is the directory name, used to refer to the repo on the CLI.
	Name string `json:"name"`
	// Path is the absolute repo root.
	Path string `json:"path"`
	// Remote is the origin URL, when it has one.
	Remote string `json:"remote,omitempty"`
	// Branch is the branch currently checked out in the source, if any.
	Branch string `json:"branch,omitempty"`
	// Origin says how the repo became known: "scan", "config" or "path".
	Origin string `json:"origin"`
}

// Scan walks each root to depth levels looking for directories containing a
// .git directory (worktree checkouts, whose .git is a file, are skipped, as
// are bare repos and anything under skip). It does not descend into a repo
// once found.
func Scan(roots []string, depth int, skip []string) []string {
	var found []string
	skipSet := map[string]bool{}
	for _, s := range skip {
		skipSet[filepath.Clean(s)] = true
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if skipSet[p] {
				return fs.SkipDir
			}
			if p != root {
				rel, _ := filepath.Rel(root, p)
				if strings.Count(filepath.ToSlash(rel), "/")+1 > depth {
					return fs.SkipDir
				}
				if strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
			}
			if info, serr := os.Lstat(filepath.Join(p, ".git")); serr == nil && info.IsDir() {
				found = append(found, p)
				return fs.SkipDir
			}
			return nil
		})
	}
	return found
}

// Repos merges scanned repos, config-listed repos and index-remembered
// repos into one sorted, de-duplicated list. Entries that are no longer
// non-bare git repos are dropped. skip excludes directories (the package
// root) from scanning.
func Repos(roots, configured []string, idx *index.Index, depth int, skip []string) []Repo {
	byPath := map[string]Repo{}
	add := func(path, origin string) {
		path = filepath.Clean(path)
		if _, ok := byPath[path]; ok {
			return
		}
		top, err := gitx.TopLevel(path)
		if err != nil {
			return
		}
		if bare, err := gitx.IsBare(top); err != nil || bare {
			return
		}
		if _, ok := byPath[top]; ok {
			return
		}
		byPath[top] = Repo{
			Name:   filepath.Base(top),
			Path:   top,
			Remote: gitx.RemoteURL(top, "origin"),
			Branch: gitx.CurrentBranch(top),
			Origin: origin,
		}
	}
	for _, p := range Scan(roots, depth, skip) {
		add(p, "scan")
	}
	for _, p := range configured {
		add(p, "config")
	}
	if idx != nil {
		for _, r := range idx.Repos {
			add(r.Path, "path")
		}
	}
	out := make([]Repo, 0, len(byPath))
	for _, r := range byPath {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Resolve turns a CLI repo argument into a repo. The argument is first
// tried as a known repo's name, then as a path to any git repo on disk. A
// path resolves to its repo root. Ambiguous names are an error.
func Resolve(arg string, known []Repo) (Repo, error) {
	var matches []Repo
	for _, r := range known {
		if r.Name == arg {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
	default:
		return Repo{}, &AmbiguousError{Name: arg, Matches: matches}
	}
	top, err := gitx.TopLevel(arg)
	if err != nil {
		return Repo{}, &NotFoundError{Arg: arg}
	}
	if bare, err := gitx.IsBare(top); err != nil || bare {
		return Repo{}, &NotFoundError{Arg: arg}
	}
	return Repo{
		Name:   filepath.Base(top),
		Path:   top,
		Remote: gitx.RemoteURL(top, "origin"),
		Branch: gitx.CurrentBranch(top),
		Origin: "path",
	}, nil
}

// NotFoundError means a repo argument matched no known name and is not a
// path to a git repo.
type NotFoundError struct{ Arg string }

// Error implements error.
func (e *NotFoundError) Error() string {
	return "repo not found: " + e.Arg + " (not a known repo name or a path to a non-bare git repo)"
}

// AmbiguousError means several known repos share a name.
type AmbiguousError struct {
	Name    string
	Matches []Repo
}

// Error implements error.
func (e *AmbiguousError) Error() string {
	var paths []string
	for _, m := range e.Matches {
		paths = append(paths, m.Path)
	}
	return "repo name " + e.Name + " is ambiguous; use a path: " + strings.Join(paths, ", ")
}
