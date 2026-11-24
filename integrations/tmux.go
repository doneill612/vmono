package integrations

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tmuxMarker ends the one line vmono adds to the user's tmux.conf. It is how
// that line is found again to update or remove it; nothing else in the file
// is ever read or changed.
const tmuxMarker = "# vmono integration: tmux"

// tmuxIntegration installs vmono's tmux bindings and turns on the "auto"
// launcher, which starts each session's harness in its own tmux session
// whenever vmono runs inside tmux.
//
// Install writes vmono.tmux to vmono's config directory and adds a single
// line to the user's tmux.conf that sources it:
//
//	source-file -q '<config dir>/integrations/tmux/vmono.tmux' # vmono integration: tmux
//
// `-q` keeps tmux quiet if the file is ever missing, for instance after vmono
// is removed without uninstalling. The tmux.conf used is the first that
// exists of ~/.tmux.conf, $XDG_CONFIG_HOME/tmux/tmux.conf and
// ~/.config/tmux/tmux.conf, which is the order tmux itself reads them in;
// when none exists, ~/.tmux.conf is created. The file is edited in place, so
// a tmux.conf that is a symlink into a dotfiles repo stays one. Nothing is
// reloaded: the user runs `tmux source-file` on that config, or restarts
// tmux, when they are ready.
var tmuxIntegration = Integration{
	Name:      "tmux",
	Summary:   "prefix+V opens vmono in a popup; sessions launch in their own tmux sessions",
	Tool:      "tmux",
	Launcher:  "auto",
	Files:     []string{"vmono.tmux"},
	install:   installTmux,
	uninstall: uninstallTmux,
	status:    tmuxStatus,
}

// TmuxConfCandidates returns the tmux.conf paths tmux reads, in its order.
func TmuxConfCandidates(e Env) []string {
	out := []string{filepath.Join(e.Home, ".tmux.conf")}
	if e.XDGConfigHome != "" {
		out = append(out, filepath.Join(e.XDGConfigHome, "tmux", "tmux.conf"))
	}
	if xdg := filepath.Join(e.Home, ".config", "tmux", "tmux.conf"); xdg != out[len(out)-1] {
		out = append(out, xdg)
	}
	return out
}

// tmuxConf returns the tmux.conf to wire into: the first that exists, or
// ~/.tmux.conf when none does.
func tmuxConf(e Env) string {
	for _, p := range TmuxConfCandidates(e) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return TmuxConfCandidates(e)[0]
}

// tmuxSourceLine returns the line added to tmux.conf for a vmono.tmux at
// path. The path is single-quoted, which tmux takes literally.
func tmuxSourceLine(path string) string {
	return fmt.Sprintf("source-file -q '%s' %s", path, tmuxMarker)
}

// installTmux writes vmono.tmux and makes sure the tmux.conf in use has
// exactly one marked line sourcing it, replacing an older one in place.
func installTmux(i Integration, e Env) ([]Change, error) {
	changes, err := writeFiles(i, e)
	if err != nil {
		return changes, err
	}
	conf := tmuxConf(e)
	line := tmuxSourceLine(filepath.Join(e.Dir(i.Name), "vmono.tmux"))
	data, err := os.ReadFile(conf)
	if err != nil && !os.IsNotExist(err) {
		return changes, err
	}
	created := os.IsNotExist(err)
	text := string(data)
	lines := splitLines(text)
	found := false
	var kept []string
	for _, l := range lines {
		if !strings.HasSuffix(strings.TrimSpace(l), tmuxMarker) {
			kept = append(kept, l)
			continue
		}
		if !found {
			kept = append(kept, line)
			found = true
		}
	}
	if !found {
		kept = append(kept, line)
	}
	next := strings.Join(kept, "\n") + "\n"
	if next == text {
		return changes, nil
	}
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return changes, err
	}
	if err := writeInPlace(conf, next); err != nil {
		return changes, err
	}
	action := "updated"
	if created {
		action = "created"
	}
	return append(changes, Change{Path: conf, Action: action}), nil
}

// uninstallTmux removes the marked line from every tmux.conf tmux could read
// and deletes vmono's own tmux directory.
func uninstallTmux(i Integration, e Env) ([]Change, error) {
	var changes []Change
	for _, conf := range TmuxConfCandidates(e) {
		data, err := os.ReadFile(conf)
		if err != nil {
			continue
		}
		var kept []string
		removed := false
		for _, l := range splitLines(string(data)) {
			if strings.HasSuffix(strings.TrimSpace(l), tmuxMarker) {
				removed = true
				continue
			}
			kept = append(kept, l)
		}
		if !removed {
			continue
		}
		next := ""
		if len(kept) > 0 {
			next = strings.Join(kept, "\n") + "\n"
		}
		if err := writeInPlace(conf, next); err != nil {
			return changes, err
		}
		changes = append(changes, Change{Path: conf, Action: "updated"})
	}
	dir := e.Dir(i.Name)
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return changes, err
		}
		changes = append(changes, Change{Path: dir, Action: "removed"})
	}
	return changes, nil
}

// tmuxStatus reports whether a tmux.conf sources vmono.tmux and whether the
// written file matches this binary.
func tmuxStatus(i Integration, e Env) Status {
	st := Status{Name: i.Name, Summary: i.Summary}
	if _, err := exec.LookPath(i.Tool); err == nil {
		st.ToolFound = true
	}
	for _, conf := range TmuxConfCandidates(e) {
		data, err := os.ReadFile(conf)
		if err == nil && strings.Contains(string(data), tmuxMarker) {
			st.Wired = conf
			break
		}
	}
	present, current := filesCurrent(i, e)
	st.Installed = st.Wired != "" && present
	st.Outdated = st.Installed && !current
	return st
}

// splitLines splits text into lines without a trailing empty one, so joining
// with "\n" and adding a final newline round-trips a well-formed file.
func splitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// writeInPlace overwrites a file through any symlink, keeping its mode. A
// write-and-rename would replace a symlinked dotfile with a plain copy.
func writeInPlace(path, content string) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(path, []byte(content), mode)
}
