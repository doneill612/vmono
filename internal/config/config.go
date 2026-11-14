// Package config loads and saves the global vmono configuration.
//
// Precedence, highest first: command-line flags, VMONO_* environment
// variables, config.toml, built-in defaults. Flags are applied by callers on
// top of the value returned from Load.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/doneill612/vmono/internal/paths"
)

// Harness describes a coding agent CLI vmono can launch. Any CLI that takes
// its working directory as its project can be a harness.
type Harness struct {
	// Command is the executable name or path.
	Command string `toml:"command"`
	// Args are passed before any arguments given to `vmono open --`.
	Args []string `toml:"args,omitempty"`
	// Adapter names the generated-file adapter ("claude-code", "opencode"),
	// or is empty for none.
	Adapter string `toml:"adapter,omitempty"`
	// Env is extra environment set when launching the harness.
	Env map[string]string `toml:"env,omitempty"`
}

// Config is the global configuration read from config.toml.
type Config struct {
	SourceRoots    []string           `toml:"source_roots"`
	Repos          []string           `toml:"repos"`
	PackageRoot    string             `toml:"package_root"`
	ScanDepth      int                `toml:"scan_depth"`
	BranchPrefix   string             `toml:"branch_prefix"`
	FetchOnCreate  bool               `toml:"fetch_on_create"`
	DefaultHarness string             `toml:"default_harness"`
	AutoCommit     bool               `toml:"auto_commit"`
	ReturnToTUI    bool               `toml:"return_to_tui"`
	Harness        map[string]Harness `toml:"harness"`

	// Loaded reports whether a config file existed on disk. It is false on
	// first run, which triggers the setup flow.
	Loaded bool `toml:"-"`
}

// DefaultHarnesses returns the built-in harness definitions used when the
// config file defines none.
func DefaultHarnesses() map[string]Harness {
	return map[string]Harness{
		"claude":   {Command: "claude", Adapter: "claude-code"},
		"opencode": {Command: "opencode", Adapter: "opencode"},
		"codex":    {Command: "codex"},
		"pi":       {Command: "pi"},
	}
}

// Defaults returns the built-in configuration.
func Defaults() Config {
	return Config{
		PackageRoot:    "~/.vmono",
		ScanDepth:      2,
		BranchPrefix:   "vmono/",
		FetchOnCreate:  true,
		DefaultHarness: "claude",
		AutoCommit:     true,
		ReturnToTUI:    runtime.GOOS == "windows",
		Harness:        DefaultHarnesses(),
	}
}

// Load reads config.toml over the defaults, then applies VMONO_* environment
// overrides. A missing file is not an error; Config.Loaded reports it.
func Load() (Config, error) {
	file, err := paths.ConfigFile()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(file)
}

// LoadFrom is Load for an explicit file path.
func LoadFrom(file string) (Config, error) {
	cfg := Defaults()
	userHarnesses := map[string]Harness{}
	data, err := os.ReadFile(file)
	switch {
	case err == nil:
		var onDisk Config
		onDisk.Harness = userHarnesses
		if _, derr := toml.Decode(string(data), &onDisk); derr != nil {
			return Config{}, fmt.Errorf("parse %s: %w", file, derr)
		}
		meta := decodeKeys(data)
		overlay(&cfg, onDisk, meta)
		cfg.Loaded = true
	case errors.Is(err, os.ErrNotExist):
	default:
		return Config{}, err
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if runtime.GOOS == "windows" {
		cfg.ReturnToTUI = true
	}
	return cfg, cfg.Validate()
}

// decodeKeys returns the set of top-level keys present in a config file so
// that explicit false/zero values override defaults.
func decodeKeys(data []byte) map[string]bool {
	var raw map[string]any
	keys := map[string]bool{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return keys
	}
	for k := range raw {
		keys[k] = true
	}
	return keys
}

// overlay copies every key present in the file over the defaults.
func overlay(dst *Config, src Config, present map[string]bool) {
	if present["source_roots"] {
		dst.SourceRoots = src.SourceRoots
	}
	if present["repos"] {
		dst.Repos = src.Repos
	}
	if present["package_root"] {
		dst.PackageRoot = src.PackageRoot
	}
	if present["scan_depth"] {
		dst.ScanDepth = src.ScanDepth
	}
	if present["branch_prefix"] {
		dst.BranchPrefix = src.BranchPrefix
	}
	if present["fetch_on_create"] {
		dst.FetchOnCreate = src.FetchOnCreate
	}
	if present["default_harness"] {
		dst.DefaultHarness = src.DefaultHarness
	}
	if present["auto_commit"] {
		dst.AutoCommit = src.AutoCommit
	}
	if present["return_to_tui"] {
		dst.ReturnToTUI = src.ReturnToTUI
	}
	if present["harness"] && len(src.Harness) > 0 {
		dst.Harness = src.Harness
	}
}

// applyEnv applies VMONO_* environment overrides.
func applyEnv(c *Config) error {
	if v := os.Getenv("VMONO_PACKAGE_ROOT"); v != "" {
		c.PackageRoot = v
	}
	if v := os.Getenv("VMONO_SOURCE_ROOTS"); v != "" {
		c.SourceRoots = filepath.SplitList(v)
	}
	if v := os.Getenv("VMONO_BRANCH_PREFIX"); v != "" {
		c.BranchPrefix = v
	}
	if v := os.Getenv("VMONO_DEFAULT_HARNESS"); v != "" {
		c.DefaultHarness = v
	}
	if v := os.Getenv("VMONO_SCAN_DEPTH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("VMONO_SCAN_DEPTH: %w", err)
		}
		c.ScanDepth = n
	}
	for name, dst := range map[string]*bool{
		"VMONO_FETCH_ON_CREATE": &c.FetchOnCreate,
		"VMONO_AUTO_COMMIT":     &c.AutoCommit,
		"VMONO_RETURN_TO_TUI":   &c.ReturnToTUI,
	} {
		if v := os.Getenv(name); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			*dst = b
		}
	}
	return nil
}

// Validate checks values that would otherwise fail confusingly later.
func (c Config) Validate() error {
	if c.PackageRoot == "" {
		return errors.New("config: package_root is empty")
	}
	if c.ScanDepth < 0 {
		return errors.New("config: scan_depth must be >= 0")
	}
	if c.DefaultHarness != "" {
		if _, ok := c.Harness[c.DefaultHarness]; !ok {
			return fmt.Errorf("config: default_harness %q is not a configured harness", c.DefaultHarness)
		}
	}
	return nil
}

// PackageRootPath returns the absolute package root.
func (c Config) PackageRootPath() (string, error) {
	return paths.Abs(c.PackageRoot)
}

// SourceRootPaths returns the absolute source roots.
func (c Config) SourceRootPaths() ([]string, error) {
	out := make([]string, 0, len(c.SourceRoots))
	for _, r := range c.SourceRoots {
		p, err := paths.Abs(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// HarnessNames returns the configured harness keys in sorted order.
func (c Config) HarnessNames() []string {
	names := make([]string, 0, len(c.Harness))
	for k := range c.Harness {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Save writes the config to the default config file, creating its directory.
func (c Config) Save() error {
	file, err := paths.ConfigFile()
	if err != nil {
		return err
	}
	return c.SaveTo(file)
}

// SaveTo writes the config to an explicit path atomically.
func (c Config) SaveTo(file string) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, bytes.TrimLeft(buf.Bytes(), "\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// SplitCSV splits a comma-separated flag value, trimming blanks.
func SplitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// UnknownHarnessError reports a harness name that is not configured.
type UnknownHarnessError struct {
	Name string
	Have []string
}

// Error implements error.
func (e *UnknownHarnessError) Error() string {
	return fmt.Sprintf("harness %q is not configured (have: %s)", e.Name, strings.Join(e.Have, ", "))
}

// ResolveHarness picks the harness to use: the explicit name (a flag), then
// the package's own, then the global default. An unknown name returns an
// *UnknownHarnessError.
func (c Config) ResolveHarness(flag, pkgHarness string) (string, Harness, error) {
	name := flag
	if name == "" {
		name = pkgHarness
	}
	if name == "" {
		name = c.DefaultHarness
	}
	if name == "" {
		return "", Harness{}, errors.New("no harness configured; pass --harness or set default_harness")
	}
	h, ok := c.Harness[name]
	if !ok {
		return "", Harness{}, &UnknownHarnessError{Name: name, Have: c.HarnessNames()}
	}
	return name, h, nil
}
