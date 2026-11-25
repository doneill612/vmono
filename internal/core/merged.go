package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/paths"
)

// How a branch's work got onto the branch it was meant for.
const (
	// MergedCommit: the branch's commits are there themselves, from a merge
	// commit or a fast-forward.
	MergedCommit = "merge"
	// MergedSquash: one commit there makes the branch's whole change.
	MergedSquash = "squash"
	// MergedRebase: each of the branch's commits has a copy there.
	MergedRebase = "rebase"
)

// mergedInto returns how branch was merged into base, or "" when it was not as
// far as can be told. A branch with no commits of its own reads as merged, so
// callers must know it had some: that it was published, say. A squash whose
// change was edited on the way in, or that resolved a conflict, is not found.
func mergedInto(src, branch, base string) string {
	tip, err := gitx.ResolveRef(src, branch)
	if err != nil {
		return ""
	}
	on, err := gitx.ResolveRef(src, base)
	if err != nil {
		return ""
	}
	if gitx.IsMerged(src, tip, on) {
		return MergedCommit
	}
	if n, err := gitx.RevCount(src, on, "--not", tip); err != nil || n == 0 {
		return ""
	}
	return merges.lookup(tip+" "+on, func() string {
		switch {
		case gitx.RebaseMerged(src, tip, on):
			return MergedRebase
		case gitx.SquashMerged(src, tip, on):
			return MergedSquash
		}
		return ""
	})
}

// mergedIntoAny returns how branch was merged into the first of bases it was
// merged into, and which that is.
func mergedIntoAny(src, branch string, bases []string) (how, base string) {
	for _, b := range bases {
		if how := mergedInto(src, branch, b); how != "" {
			return how, b
		}
	}
	return "", ""
}

// maxMerges caps the remembered answers.
const maxMerges = 500

// mergeCache remembers how one commit was merged into another. Finding out
// compares patches, which takes long enough in a big repo to notice on every
// status, and the answer for two commits never changes. It is kept in the state
// directory, best effort: one that cannot be read or written is worked out
// again.
type mergeCache struct {
	mu   sync.Mutex
	file string
	how  map[string]string
}

// merges is the cache every lookup of the process shares.
var merges mergeCache

// lookup returns the remembered answer for key, or works it out.
func (m *mergeCache) lookup(key string, find func() string) string {
	m.mu.Lock()
	m.load()
	how, ok := m.how[key]
	m.mu.Unlock()
	if ok {
		return how
	}
	how = find()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.how {
		if len(m.how) < maxMerges {
			break
		}
		delete(m.how, k)
	}
	m.how[key] = how
	m.save()
	return how
}

// load reads the cache the first time, and again when the state directory is
// another one.
func (m *mergeCache) load() {
	dir, err := paths.StateDir()
	if err != nil {
		dir = ""
	}
	file := ""
	if dir != "" {
		file = filepath.Join(dir, "merged.json")
	}
	if m.how != nil && m.file == file {
		return
	}
	m.file, m.how = file, map[string]string{}
	if file == "" {
		return
	}
	if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &m.how) != nil || m.how == nil {
		m.how = map[string]string{}
	}
}

// save writes the cache.
func (m *mergeCache) save() {
	if m.file == "" {
		return
	}
	data, err := json.Marshal(m.how)
	if err != nil || os.MkdirAll(filepath.Dir(m.file), 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.file), "merged-*.tmp")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), m.file) != nil {
		_ = os.Remove(tmp.Name())
	}
}
