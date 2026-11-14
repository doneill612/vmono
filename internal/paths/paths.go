// Package paths resolves where vmono keeps its global config and state on
// each operating system, and expands user-supplied paths.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigDir returns the directory holding config.toml.
//
// Resolution order: VMONO_CONFIG_DIR, then %APPDATA%\vmono on Windows, then
// $XDG_CONFIG_HOME/vmono, then ~/.config/vmono. The override exists so tests
// and wrapper tooling never touch a real user config.
func ConfigDir() (string, error) {
	if d := os.Getenv("VMONO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "vmono"), nil
		}
		return "", errors.New("APPDATA is not set")
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "vmono"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "vmono"), nil
}

// StateDir returns the directory holding index.json.
//
// Resolution order: VMONO_STATE_DIR, then %LOCALAPPDATA%\vmono on Windows,
// then $XDG_STATE_HOME/vmono, then ~/.local/state/vmono.
func StateDir() (string, error) {
	if d := os.Getenv("VMONO_STATE_DIR"); d != "" {
		return d, nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "vmono"), nil
		}
		return "", errors.New("LOCALAPPDATA is not set")
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "vmono"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "vmono"), nil
}

// ConfigFile returns the full path of config.toml.
func ConfigFile() (string, error) {
	d, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

// IndexFile returns the full path of index.json.
func IndexFile() (string, error) {
	d, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "index.json"), nil
}

// Expand replaces a leading "~" with the user's home directory and returns a
// cleaned path. Relative paths are returned cleaned but otherwise unchanged.
func Expand(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return filepath.Clean(p), nil
}

// Abs expands a leading "~" and makes the result absolute.
func Abs(p string) (string, error) {
	e, err := Expand(p)
	if err != nil {
		return "", err
	}
	return filepath.Abs(e)
}

// Contract is the inverse of Expand for display and storage: a path inside
// the home directory is rewritten with a leading "~/". Paths are stored with
// forward slashes after the tilde so manifests stay portable.
func Contract(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return p
	}
	return "~/" + filepath.ToSlash(rel)
}
