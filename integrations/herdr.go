package integrations

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// herdrBegin and herdrEnd are the prefixes of the lines around the block vmono
// adds to herdr's config. Nothing outside them is ever changed.
const (
	herdrBegin = "# vmono:begin"
	herdrEnd   = "# vmono:end"
)

// herdrKey is the key in the embedded block.
const herdrKey = "prefix+shift+v"

// herdrIntegration binds a key in herdr that opens the vmono TUI in a popup,
// and turns on the "auto" launcher, which starts each session's harness in its
// own herdr workspace whenever vmono runs inside herdr.
//
// herdr's config has no way to include another file, so the binding itself is
// appended to it, as one block between marker lines. Install refuses a key the
// config already uses, and a config it cannot parse. VMONO_LAUNCHER=herdr in
// the block makes sure a harness is never started inside the popup.
var herdrIntegration = Integration{
	Name:      "herdr",
	Summary:   "prefix+shift+v opens vmono in a popup; sessions launch in their own herdr workspaces",
	Tool:      "herdr",
	Launcher:  "auto",
	Reload:    "herdr server reload-config",
	Files:     []string{"keys.toml"},
	install:   installHerdr,
	uninstall: uninstallHerdr,
	status:    herdrStatus,
}

// herdrConf returns herdr's config file.
func herdrConf(e Env) string {
	switch {
	case e.HerdrConfig != "":
		return e.HerdrConfig
	case e.XDGConfigHome != "":
		return filepath.Join(e.XDGConfigHome, "herdr", "config.toml")
	}
	return filepath.Join(e.Home, ".config", "herdr", "config.toml")
}

// herdrBlock returns the block that binds key.
func herdrBlock(i Integration, key string) (string, error) {
	b, err := i.Content("keys.toml")
	if err != nil {
		return "", err
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.Replace(text, fmt.Sprintf("%q", herdrKey), fmt.Sprintf("%q", key), 1), nil
}

// cutHerdrBlock splits vmono's block out of a config. The block is "" when
// there is none, and a begin marker with no end is an error, since what
// follows it may be the user's.
func cutHerdrBlock(text string) (rest, block string, err error) {
	var kept, cut []string
	in := false
	for _, line := range strings.SplitAfter(text, "\n") {
		if strings.HasPrefix(line, herdrBegin) {
			in = true
		}
		if in {
			cut = append(cut, line)
		} else {
			kept = append(kept, line)
		}
		if strings.HasPrefix(line, herdrEnd) {
			in = false
		}
	}
	if in {
		return "", "", fmt.Errorf("a %q line has no %q after it; fix or remove it by hand", herdrBegin, herdrEnd)
	}
	return strings.Join(kept, ""), strings.Join(cut, ""), nil
}

// blockKey returns the key a block binds, or "".
func blockKey(block string) string {
	var conf struct {
		Keys struct {
			Command []struct{ Key string }
		}
	}
	if _, err := toml.Decode(block, &conf); err != nil || len(conf.Keys.Command) == 0 {
		return ""
	}
	return conf.Keys.Command[0].Key
}

// bindsKey reports whether a decoded keys table uses key anywhere.
func bindsKey(v any, key string) bool {
	switch t := v.(type) {
	case string:
		return strings.EqualFold(strings.ReplaceAll(t, " ", ""), strings.ReplaceAll(key, " ", ""))
	case []any:
		for _, item := range t {
			if bindsKey(item, key) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range t {
			if bindsKey(item["key"], key) {
				return true
			}
		}
	case map[string]any:
		for _, item := range t {
			if bindsKey(item, key) {
				return true
			}
		}
	}
	return false
}

// installHerdr makes sure herdr's config ends with exactly one current block.
// A block already there keeps its key unless another is asked for.
func installHerdr(i Integration, e Env) ([]Change, error) {
	if runtime.GOOS == "windows" {
		return nil, errors.New("the herdr integration is not supported on Windows")
	}
	conf := herdrConf(e)
	data, err := os.ReadFile(conf)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	created := os.IsNotExist(err)
	rest, old, err := cutHerdrBlock(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", conf, err)
	}
	key := e.Key
	if key == "" {
		key = blockKey(old)
	}
	if key == "" {
		key = herdrKey
	}
	var raw map[string]any
	if _, err := toml.Decode(rest, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", conf, err)
	}
	if bindsKey(raw["keys"], key) {
		return nil, fmt.Errorf("%s already binds %s; pick another key with --key", conf, key)
	}
	block, err := herdrBlock(i, key)
	if err != nil {
		return nil, err
	}
	if old == block {
		return nil, nil
	}
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	next := rest + block
	if _, err := toml.Decode(next, &raw); err != nil {
		return nil, fmt.Errorf("%s cannot take another [[keys.command]] table: %w", conf, err)
	}
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return nil, err
	}
	if err := writeInPlace(conf, next); err != nil {
		return nil, err
	}
	action := "updated"
	if created {
		action = "created"
	}
	return []Change{{Path: conf, Action: action}}, nil
}

// uninstallHerdr removes the block.
func uninstallHerdr(_ Integration, e Env) ([]Change, error) {
	conf := herdrConf(e)
	data, err := os.ReadFile(conf)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rest, block, err := cutHerdrBlock(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", conf, err)
	}
	if block == "" {
		return nil, nil
	}
	if err := writeInPlace(conf, rest); err != nil {
		return nil, err
	}
	return []Change{{Path: conf, Action: "updated"}}, nil
}

// herdrStatus reports whether herdr's config holds the block, and whether the
// block is the one this binary writes.
func herdrStatus(i Integration, e Env) Status {
	st := Status{Name: i.Name, Summary: i.Summary}
	if _, err := exec.LookPath(i.Tool); err == nil {
		st.ToolFound = true
	}
	conf := herdrConf(e)
	data, err := os.ReadFile(conf)
	if err != nil || !strings.Contains("\n"+string(data), "\n"+herdrBegin) {
		return st
	}
	st.Installed, st.Wired = true, conf
	_, block, err := cutHerdrBlock(string(data))
	want, werr := herdrBlock(i, blockKey(block))
	st.Outdated = err != nil || werr != nil || block != want
	return st
}
