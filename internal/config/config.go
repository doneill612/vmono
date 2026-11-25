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
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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
	// Adapter names the generated-file adapter ("claude-code", "opencode",
	// "agent-skills"), or is empty for none.
	Adapter string `toml:"adapter,omitempty"`
	// Env is extra environment set when launching the harness.
	Env map[string]string `toml:"env,omitempty"`
	// InitArgs are passed after Args when the harness is started to initialize
	// a package. PromptArg in one stands for the first prompt. Empty means the
	// harness starts with no prompt and the user pastes it.
	InitArgs []string `toml:"init_args,omitempty"`
}

// PromptArg is replaced by the first prompt in a harness's InitArgs.
const PromptArg = "{prompt}"

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
	Launcher       string             `toml:"launcher"`
	TmuxSwitch     bool               `toml:"tmux_switch"`
	HerdrFocus     bool               `toml:"herdr_focus"`
	Harness        map[string]Harness `toml:"harness"`
	// Hooks are the commands run at points in every session's life, by name.
	Hooks map[string]Hook `toml:"hooks,omitempty"`

	// NoHooks turns every hook off, members' setup and teardown included. It
	// comes from VMONO_NO_HOOKS or --no-hooks and is never saved.
	NoHooks bool `toml:"-"`

	// Notices are settings in the config file that were ignored, for the front
	// ends to point out.
	Notices []string `toml:"-"`

	// Loaded reports whether a config file existed on disk. It is false on
	// first run, which triggers the setup flow.
	Loaded bool `toml:"-"`
}

// Names of the global hooks.
const (
	// HookPostCreate runs once a session has been created.
	HookPostCreate = "post_create"
	// HookPreRemove runs before a session is removed.
	HookPreRemove = "pre_remove"
	// hookPostFinish ran once a session's work had landed on other branches,
	// which no longer happens. A config that still has it loads without it.
	hookPostFinish = "post_finish"
)

// HookNames lists every global hook.
func HookNames() []string { return []string{HookPostCreate, HookPreRemove} }

// What happens when a hook's command fails.
const (
	// OnFailureWarn reports it and carries on; the default.
	OnFailureWarn = "warn"
	// OnFailureAbort fails the operation the hook belongs to.
	OnFailureAbort = "abort"
)

// Hook is one global hook. In config.toml it is a command, or a table when it
// needs more than that; saving the config always writes the table.
type Hook struct {
	Command string `toml:"command"`
	// Timeout is a Go duration such as "30s"; empty means the default.
	Timeout string `toml:"timeout,omitempty"`
	// OnFailure is OnFailureWarn or OnFailureAbort; empty means warn.
	OnFailure string `toml:"on_failure,omitempty"`
}

// UnmarshalTOML implements toml.Unmarshaler.
func (h *Hook) UnmarshalTOML(v any) error {
	if s, ok := v.(string); ok {
		*h = Hook{Command: s}
		return nil
	}
	table, ok := v.(map[string]any)
	if !ok {
		return errors.New("a hook is a command, or a table with one")
	}
	*h = Hook{}
	for key, val := range table {
		dst := map[string]*string{"command": &h.Command, "timeout": &h.Timeout, "on_failure": &h.OnFailure}[key]
		if dst == nil {
			return fmt.Errorf("unknown hook key %q (have command, timeout, on_failure)", key)
		}
		if *dst, ok = val.(string); !ok {
			return fmt.Errorf("hook key %q must be a string", key)
		}
	}
	return nil
}

// Limit returns the hook's timeout, zero when it sets none.
func (h Hook) Limit() time.Duration {
	d, _ := time.ParseDuration(h.Timeout)
	return d
}

// validate checks one hook's values.
func (h Hook) validate() error {
	if strings.TrimSpace(h.Command) == "" {
		return errors.New("command is empty")
	}
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("timeout %q is not a duration such as 30s", h.Timeout)
		}
	}
	if h.OnFailure != "" && h.OnFailure != OnFailureWarn && h.OnFailure != OnFailureAbort {
		return fmt.Errorf("on_failure %q is not %s or %s", h.OnFailure, OnFailureWarn, OnFailureAbort)
	}
	return nil
}

// Launcher names say where a harness is started when a session is opened.
//
// LauncherDirect runs it in the terminal vmono was started from, replacing
// vmono on macOS and Linux. LauncherTmux gives every session its own tmux
// session, named after it, and switches to it, so several sessions run side
// by side and the TUI stays a control panel. LauncherHerdr does the same with
// herdr workspaces. LauncherAuto picks herdr or tmux when vmono itself runs
// inside one ($HERDR_ENV or $TMUX is set) and direct otherwise, which is what
// `vmono integration install` sets so a terminal outside them keeps working as
// before.
const (
	LauncherDirect = "direct"
	LauncherTmux   = "tmux"
	LauncherHerdr  = "herdr"
	LauncherAuto   = "auto"
)

// An empty launcher, as in a Config built by hand rather than from Defaults,
// means LauncherDirect.
//
// Launchers lists every valid launcher value, in the order they are offered.
func Launchers() []string { return []string{LauncherDirect, LauncherAuto, LauncherTmux, LauncherHerdr} }

// DefaultHarnesses returns the built-in harness definitions used when the
// config file defines none.
func DefaultHarnesses() map[string]Harness {
	return map[string]Harness{
		// The mode is explicit because in plan mode, which a user may have as
		// the default, Claude Code would not record anything.
		"claude": {Command: "claude", Adapter: "claude-code",
			InitArgs: []string{"--permission-mode", "default", PromptArg}},
		"opencode": {Command: "opencode", Adapter: "opencode",
			InitArgs: []string{"--prompt", PromptArg}},
		"codex": {Command: "codex", Adapter: "agent-skills",
			InitArgs: []string{"--sandbox", "read-only", "--ask-for-approval", "on-request", PromptArg}},
		// Pi has no permission rules, and needs bash to record.
		"pi": {Command: "pi", Adapter: "agent-skills",
			InitArgs: []string{"--tools", "read,grep,find,ls,bash", PromptArg}},
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
		Launcher:       LauncherDirect,
		TmuxSwitch:     true,
		HerdrFocus:     true,
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
		backfillHarnesses(&cfg)
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
	if present["launcher"] {
		dst.Launcher = src.Launcher
	}
	if present["tmux_switch"] {
		dst.TmuxSwitch = src.TmuxSwitch
	}
	if present["herdr_focus"] {
		dst.HerdrFocus = src.HerdrFocus
	}
	if present["hooks"] {
		dst.Hooks = src.Hooks
		if _, ok := dst.Hooks[hookPostFinish]; ok {
			delete(dst.Hooks, hookPostFinish)
			dst.Notices = append(dst.Notices, "config: the "+hookPostFinish+" hook is ignored, since sessions are no longer finished; remove it")
		}
	}
	if present["harness"] && len(src.Harness) > 0 {
		dst.Harness = src.Harness
	}
}

// backfillHarnesses gives a built-in harness its default adapter and init
// arguments when the config file lists it without them. A saved config carries the whole harness
// table, so it would otherwise keep a harness written before one of these
// existed from ever getting it. Only an
// entry that still runs the built-in command is touched, so a harness the
// user repointed at something else is left alone.
func backfillHarnesses(c *Config) {
	for name, def := range DefaultHarnesses() {
		h, ok := c.Harness[name]
		if !ok || h.Command != def.Command {
			continue
		}
		if h.Adapter == "" {
			h.Adapter = def.Adapter
		}
		if len(h.InitArgs) == 0 {
			h.InitArgs = def.InitArgs
		}
		c.Harness[name] = h
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
	if v := os.Getenv("VMONO_LAUNCHER"); v != "" {
		c.Launcher = v
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
		"VMONO_TMUX_SWITCH":     &c.TmuxSwitch,
		"VMONO_HERDR_FOCUS":     &c.HerdrFocus,
		"VMONO_NO_HOOKS":        &c.NoHooks,
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
	if c.Launcher != "" && !slices.Contains(Launchers(), c.Launcher) {
		return fmt.Errorf("config: launcher %q is not one of %s", c.Launcher, strings.Join(Launchers(), ", "))
	}
	for name, h := range c.Hooks {
		if !slices.Contains(HookNames(), name) {
			return fmt.Errorf("config: unknown hook %q (have %s)", name, strings.Join(HookNames(), ", "))
		}
		if err := h.validate(); err != nil {
			return fmt.Errorf("config: hook %s: %w", name, err)
		}
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
