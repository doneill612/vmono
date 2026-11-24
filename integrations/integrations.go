// Package integrations holds the files vmono installs into other tools, and
// the logic that installs them.
//
// Each subdirectory is one integration and is embedded in the vmono binary,
// so `vmono integration install <name>` always writes the files that match
// the version being run, the same way the Claude Code plugin is rebuilt from
// the binary on every launch. The files are plain config for the tool
// (tmux key bindings today; a herdr plugin manifest is the next one planned)
// and are meant to be read: this directory is the documentation of what an
// install changes.
//
// An integration has two halves. The files here make the tool reach vmono
// (a tmux key that opens the TUI in a popup). The other half is a launcher
// in internal/launcher that makes vmono reach the tool (starting a session's
// harness in its own tmux session). Installing an integration names the
// launcher it pairs with, and the CLI turns it on in vmono's config.
//
// Installs only ever add one marked line to a file the user owns (such as
// tmux.conf) and keep everything else in vmono's own config directory, so
// uninstalling removes exactly what was added and nothing more.
package integrations

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

//go:embed tmux
var files embed.FS

// Env is where an integration reads and writes. The CLI fills it from the
// real environment; tests point it at temporary directories.
type Env struct {
	// ConfigDir is vmono's config directory; an integration's own files go
	// under ConfigDir/integrations/<name>.
	ConfigDir string
	// Home is the user's home directory.
	Home string
	// XDGConfigHome is $XDG_CONFIG_HOME, or "" when unset.
	XDGConfigHome string
}

// Dir returns the directory an integration's own files are written to.
func (e Env) Dir(name string) string {
	return filepath.Join(e.ConfigDir, "integrations", name)
}

// Status is what `vmono integration ls` reports for one integration.
type Status struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	// Installed is true when the integration's files are written and wired
	// into the tool.
	Installed bool `json:"installed"`
	// Outdated is true when installed files differ from the ones in this
	// binary; installing again rewrites them.
	Outdated bool `json:"outdated"`
	// ToolFound is true when the tool's executable is on PATH.
	ToolFound bool `json:"tool_found"`
	// Wired is the user file that sources the integration, when installed.
	Wired string `json:"wired,omitempty"`
}

// Change is one file an install or uninstall touched.
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// Integration describes one tool vmono integrates with.
type Integration struct {
	// Name is what the user types: `vmono integration install <name>`.
	Name string
	// Summary is a one-line description for listings.
	Summary string
	// Tool is the executable the integration is for.
	Tool string
	// Launcher is the config.Launcher* value an install turns on, or "" when
	// the integration pairs with no launcher.
	Launcher string
	// Files lists the embedded files, relative to the integration's own
	// directory, in the order `vmono integration print` shows them.
	Files []string

	install   func(Integration, Env) ([]Change, error)
	uninstall func(Integration, Env) ([]Change, error)
	status    func(Integration, Env) Status
}

// Install writes the integration's files and wires them into the tool. It is
// idempotent: installing again rewrites the files and changes nothing else.
func (i Integration) Install(e Env) ([]Change, error) { return i.install(i, e) }

// Uninstall removes what Install added.
func (i Integration) Uninstall(e Env) ([]Change, error) { return i.uninstall(i, e) }

// Status reports whether the integration is installed and current.
func (i Integration) Status(e Env) Status { return i.status(i, e) }

// Content returns the embedded content of one of the integration's files.
func (i Integration) Content(file string) ([]byte, error) {
	return files.ReadFile(i.Name + "/" + file)
}

// registry holds every integration by name.
var registry = map[string]Integration{
	tmuxIntegration.Name: tmuxIntegration,
}

// All returns every integration, sorted by name.
func All() []Integration {
	out := make([]Integration, 0, len(registry))
	for _, i := range registry {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Get returns the integration with the given name.
func Get(name string) (Integration, error) {
	i, ok := registry[name]
	if !ok {
		names := make([]string, 0, len(registry))
		for _, i := range All() {
			names = append(names, i.Name)
		}
		return Integration{}, fmt.Errorf("unknown integration %q (have: %v)", name, names)
	}
	return i, nil
}

// writeFiles writes every embedded file of an integration under its own
// directory and reports which ones were created or updated. Files whose
// content already matches are left alone.
func writeFiles(i Integration, e Env) ([]Change, error) {
	var changes []Change
	for _, f := range i.Files {
		want, err := i.Content(f)
		if err != nil {
			return changes, err
		}
		dst := filepath.Join(e.Dir(i.Name), filepath.FromSlash(f))
		have, err := os.ReadFile(dst)
		action := "updated"
		switch {
		case err == nil && string(have) == string(want):
			continue
		case os.IsNotExist(err):
			action = "created"
		case err != nil:
			return changes, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return changes, err
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return changes, err
		}
		changes = append(changes, Change{Path: dst, Action: action})
	}
	return changes, nil
}

// filesCurrent reports whether every embedded file is written, and whether
// all of them match this binary.
func filesCurrent(i Integration, e Env) (present, current bool) {
	present, current = true, true
	for _, f := range i.Files {
		want, err := i.Content(f)
		if err != nil {
			return false, false
		}
		have, err := os.ReadFile(filepath.Join(e.Dir(i.Name), filepath.FromSlash(f)))
		if err != nil {
			present, current = false, false
			continue
		}
		if string(have) != string(want) {
			current = false
		}
	}
	return present, current
}
