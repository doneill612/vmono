// Package index manages index.json: a cache of known packages, launch
// history, and repos added by path. It is only a cache; `vmono doctor`
// rebuilds it by scanning the package root for manifests.
package index

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/doneill612/vmono/internal/paths"
)

// MaxLaunches caps the stored launch history.
const MaxLaunches = 1000

// Launch records one harness launch, used to sort Home and, later, to weight
// the relationship graph by use and recency.
type Launch struct {
	Package string    `json:"package"`
	Session string    `json:"session,omitempty"`
	Harness string    `json:"harness"`
	At      time.Time `json:"at"`
}

// Package is the cached summary of a known package. LastOpened is the most
// recent launch of any of its sessions; Sessions holds each session's own
// last-opened time.
type Package struct {
	Name       string               `json:"name"`
	Path       string               `json:"path"`
	LastOpened time.Time            `json:"last_opened,omitempty"`
	Sessions   map[string]time.Time `json:"sessions,omitempty"`
}

// Repo is a repo known to vmono outside config: added by path, or found by
// scanning a source root.
type Repo struct {
	Path   string `json:"path"`
	Remote string `json:"remote,omitempty"`
}

// Index is the on-disk cache.
type Index struct {
	Version  int                `json:"version"`
	Packages map[string]Package `json:"packages"`
	Repos    []Repo             `json:"repos"`
	Launches []Launch           `json:"launches"`

	file string
}

// Load reads the index from the default location. A missing or unreadable
// file yields an empty index: it is a cache, never authoritative.
func Load() (*Index, error) {
	file, err := paths.IndexFile()
	if err != nil {
		return nil, err
	}
	return LoadFrom(file), nil
}

// LoadFrom reads an index from file, returning an empty one on any problem.
func LoadFrom(file string) *Index {
	idx := &Index{Version: 1, Packages: map[string]Package{}, file: file}
	data, err := os.ReadFile(file)
	if err != nil {
		return idx
	}
	var onDisk Index
	if json.Unmarshal(data, &onDisk) != nil {
		return idx
	}
	idx.Repos = onDisk.Repos
	idx.Launches = onDisk.Launches
	if onDisk.Packages != nil {
		idx.Packages = onDisk.Packages
	}
	return idx
}

// Save writes the index atomically, creating its directory.
func (i *Index) Save() error {
	if i.file == "" {
		return errors.New("index has no file")
	}
	if err := os.MkdirAll(filepath.Dir(i.file), 0o755); err != nil {
		return err
	}
	if len(i.Launches) > MaxLaunches {
		i.Launches = i.Launches[len(i.Launches)-MaxLaunches:]
	}
	data, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, i.file)
}

// SetPackage records a package, preserving its last-opened time.
func (i *Index) SetPackage(name, path string) {
	p := i.Packages[name]
	p.Name, p.Path = name, path
	i.Packages[name] = p
}

// RemovePackage forgets a package.
func (i *Index) RemovePackage(name string) { delete(i.Packages, name) }

// RecordLaunch appends a launch record and updates the last-opened time of
// the session and of its package.
func (i *Index) RecordLaunch(pkg, session, harness string, at time.Time) {
	i.Launches = append(i.Launches, Launch{Package: pkg, Session: session, Harness: harness, At: at})
	p := i.Packages[pkg]
	p.Name = pkg
	p.LastOpened = at
	if session != "" {
		if p.Sessions == nil {
			p.Sessions = map[string]time.Time{}
		}
		p.Sessions[session] = at
	}
	i.Packages[pkg] = p
}

// RemoveSession forgets a session's last-opened time. The package's own
// last-opened time is left alone: it records when the package was last used,
// not which session was.
func (i *Index) RemoveSession(pkg, session string) {
	p, ok := i.Packages[pkg]
	if !ok {
		return
	}
	delete(p.Sessions, session)
	i.Packages[pkg] = p
}

// SessionOpened returns when a session was last launched, or the zero time.
func (i *Index) SessionOpened(pkg, session string) time.Time {
	return i.Packages[pkg].Sessions[session]
}

// AddRepo remembers a repo added by path. It reports whether it was new.
func (i *Index) AddRepo(r Repo) bool {
	for j, existing := range i.Repos {
		if existing.Path == r.Path {
			if r.Remote != "" {
				i.Repos[j].Remote = r.Remote
			}
			return false
		}
	}
	i.Repos = append(i.Repos, r)
	return true
}

// SortedPackages returns packages most recently opened first, then by name.
func (i *Index) SortedPackages() []Package {
	out := make([]Package, 0, len(i.Packages))
	for _, p := range i.Packages {
		out = append(out, p)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].LastOpened.Equal(out[b].LastOpened) {
			return out[a].LastOpened.After(out[b].LastOpened)
		}
		return out[a].Name < out[b].Name
	})
	return out
}
