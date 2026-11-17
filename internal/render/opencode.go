package render

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/doneill612/vmono/internal/manifest"
)

// openCodeConfigPath is the generated OpenCode project config.
const openCodeConfigPath = "opencode.json"

// openCodeSchema is OpenCode's published config schema.
const openCodeSchema = "https://opencode.ai/config.json"

// ocRule is one OpenCode permission, normalized across the 1.x object form
// and the V2 array form. Action uses the 1.x name ("bash"), which becomes
// "shell" when written for V2.
type ocRule struct {
	Action   string
	Resource string
	Effect   Effect
}

// ocActions are the permission actions vmono merges from members. Others
// are left to the user's own config.
var ocActions = map[string]bool{"edit": true, "read": true, "bash": true}

// ocPathActions are the actions whose resource is a file path.
var ocPathActions = map[string]bool{"edit": true, "read": true}

// ocRules is an ordered rule list where order matters (last match wins) and
// a repeated action+resource keeps the strictest effect.
type ocRules struct {
	rules []ocRule
}

// add appends a rule. If the same action+resource exists its effect is
// upgraded in place; with last set it also moves to the end so it wins
// over every other rule.
func (r *ocRules) add(rule ocRule, last bool) {
	for i, ex := range r.rules {
		if ex.Action == rule.Action && ex.Resource == rule.Resource {
			if ex.Effect > rule.Effect {
				rule.Effect = ex.Effect
			}
			if last {
				r.rules = append(r.rules[:i], r.rules[i+1:]...)
				r.rules = append(r.rules, rule)
			} else {
				r.rules[i].Effect = rule.Effect
			}
			return
		}
	}
	r.rules = append(r.rules, rule)
}

// RewriteOpenCodeResource scopes a member's resource to its folder. Path
// actions get the member path prefixed; OpenCode's "*" already matches
// across "/", so patterns need no depth tricks. It returns ok=false for a
// non-path rule that would widen access across the package, such as an
// allow-everything bash rule.
func RewriteOpenCodeResource(memberPath, action, resource string, e Effect) (string, bool) {
	if !ocPathActions[action] {
		if e == EffectAllow && (resource == "*" || resource == "") {
			return "", false
		}
		return resource, true
	}
	memberPath = path.Clean(strings.ReplaceAll(memberPath, `\`, "/"))
	rest := strings.TrimPrefix(strings.TrimPrefix(resource, "./"), "/")
	if rest == "" {
		rest = "*"
	}
	return memberPath + "/" + rest, true
}

// renderOpenCode writes opencode.json, member skills and nothing else.
// Member permission rules come first in manifest order, then vmono's own so
// that, with OpenCode's last-match-wins, the `_ref/**` edit deny and the
// ask rules for `vmono rm` / `vmono delete` / `vmono session rm` /
// `vmono session finish` cannot be overridden.
//
// Unlike the spec's illustrative `"*": "allow"`, no catch-all edit allow is
// written: it would silently override a user's own stricter edit setting.
// Unmatched paths fall through to the user's configuration.
func renderOpenCode(w *writer, m *manifest.Manifest, v2 bool) error {
	rules := &ocRules{}
	for _, mem := range m.Members {
		for _, r := range readOpenCodeMember(filepath.Join(w.dir, filepath.FromSlash(mem.Path))) {
			if res, keep := RewriteOpenCodeResource(mem.Path, r.Action, r.Resource, r.Effect); keep {
				rules.add(ocRule{Action: r.Action, Resource: res, Effect: r.Effect}, false)
			}
		}
	}
	for _, r := range vmonoOpenCodeRules(w.dir, v2) {
		rules.add(r, true)
	}

	var doc any
	if v2 {
		doc = openCodeV2Doc(rules)
	} else {
		doc = openCodeV1Doc(rules)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := w.file(openCodeConfigPath, string(data)+"\n"); err != nil {
		return err
	}
	if err := writeBundledSkill(w, ".opencode/skills"); err != nil {
		return err
	}
	return syncMemberSkills(w, m, skillRoot{dest: ".opencode/skills", sources: []string{".opencode/skills", ".claude/skills"}})
}

// vmonoOpenCodeRules returns the rules vmono always keeps. V2 matches whole
// normalized paths and does not document what they are relative to, so the
// `_ref` deny is written both relative and absolute.
func vmonoOpenCodeRules(pkgDir string, v2 bool) []ocRule {
	rules := []ocRule{{"edit", "_ref/**", EffectDeny}}
	if v2 {
		abs := strings.ReplaceAll(pkgDir, `\`, "/")
		rules = append(rules, ocRule{"edit", strings.TrimRight(abs, "/") + "/_ref/**", EffectDeny})
	}
	return append(rules,
		ocRule{"bash", "vmono rm *", EffectAsk},
		ocRule{"bash", "vmono delete *", EffectAsk},
		ocRule{"bash", "vmono session rm *", EffectAsk},
		ocRule{"bash", "vmono session finish *", EffectAsk},
	)
}

// orderedObject is a JSON object that keeps insertion order, which
// OpenCode's last-match-wins rules depend on.
type orderedObject struct {
	keys []string
	vals map[string]any
}

// set adds or replaces a key, keeping first-insertion position.
func (o *orderedObject) set(k string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// MarshalJSON writes keys in insertion order.
func (o orderedObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// openCodeV1Doc builds the 1.x config: permission.<action> objects mapping
// a pattern to its effect, in rule order.
func openCodeV1Doc(rules *ocRules) any {
	perm := &orderedObject{}
	byAction := map[string]*orderedObject{}
	var actions []string
	for _, r := range rules.rules {
		obj, ok := byAction[r.Action]
		if !ok {
			obj = &orderedObject{}
			byAction[r.Action] = obj
			actions = append(actions, r.Action)
		}
		obj.set(r.Resource, r.Effect.String())
	}
	for _, a := range actions {
		perm.set(a, byAction[a])
	}
	doc := &orderedObject{}
	doc.set("$schema", openCodeSchema)
	doc.set("permission", perm)
	return doc
}

// openCodeV2Doc builds the V2 config: a permissions array of
// {action, resource, effect}, with bash renamed to shell.
func openCodeV2Doc(rules *ocRules) any {
	list := make([]map[string]string, 0, len(rules.rules))
	for _, r := range rules.rules {
		action := r.Action
		if action == "bash" {
			action = "shell"
		}
		list = append(list, map[string]string{"action": action, "resource": r.Resource, "effect": r.Effect.String()})
	}
	doc := &orderedObject{}
	doc.set("$schema", openCodeSchema)
	doc.set("permissions", list)
	return doc
}

// readOpenCodeMember reads a member's opencode.json in either format.
// Anything unreadable or unrecognized yields no rules.
func readOpenCodeMember(memberDir string) []ocRule {
	data, err := os.ReadFile(filepath.Join(memberDir, openCodeConfigPath))
	if err != nil {
		return nil
	}
	var raw struct {
		Permission  map[string]json.RawMessage `json:"permission"`
		Permissions []struct {
			Action   string `json:"action"`
			Resource string `json:"resource"`
			Effect   string `json:"effect"`
		} `json:"permissions"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var out []ocRule
	for _, p := range raw.Permissions {
		action := p.Action
		if action == "shell" {
			action = "bash"
		}
		if e := ParseEffect(p.Effect); e != 0 && ocActions[action] {
			out = append(out, ocRule{action, p.Resource, e})
		}
	}
	for action, msg := range raw.Permission {
		if !ocActions[action] {
			continue
		}
		var simple string
		if json.Unmarshal(msg, &simple) == nil {
			if e := ParseEffect(simple); e != 0 {
				out = append(out, ocRule{action, "*", e})
			}
			continue
		}
		// Objects have no stable key order through a Go map; decode the
		// token stream to keep the file's order, which matters for
		// last-match-wins.
		out = append(out, decodeOrderedPatterns(action, msg)...)
	}
	return out
}

// decodeOrderedPatterns reads a {"pattern": "effect"} object in file order.
func decodeOrderedPatterns(action string, msg json.RawMessage) []ocRule {
	dec := json.NewDecoder(bytes.NewReader(msg))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var out []ocRule
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return out
		}
		var v string
		if err := dec.Decode(&v); err != nil {
			continue
		}
		if e := ParseEffect(v); e != 0 {
			out = append(out, ocRule{action, k.(string), e})
		}
	}
	return out
}

var (
	ocVersionMu    sync.Mutex
	ocVersionCache = map[string]bool{}
)

// OpenCodeV2 reports whether the installed OpenCode (the harness command,
// e.g. "opencode") is major version 2 or newer, which uses the permissions
// array. A missing or unparseable binary is treated as 1.x. Results are
// cached per command for the life of the process.
func OpenCodeV2(command string) bool {
	ocVersionMu.Lock()
	defer ocVersionMu.Unlock()
	if v, ok := ocVersionCache[command]; ok {
		return v
	}
	v := detectOpenCodeV2(command)
	ocVersionCache[command] = v
	return v
}

// detectOpenCodeV2 runs `<command> --version` with a short timeout.
func detectOpenCodeV2(command string) bool {
	bin, err := exec.LookPath(command)
	if err != nil {
		return false
	}
	cmd := exec.Command(bin, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return false
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		return false
	}
	return parseMajor(out.String()) >= 2
}

// parseMajor extracts the major version from text like "opencode 2.0.1" or
// "v1.4.0". It returns 0 when none is found.
func parseMajor(s string) int {
	for _, f := range strings.Fields(s) {
		f = strings.TrimPrefix(f, "v")
		head, _, _ := strings.Cut(f, ".")
		if n, err := strconv.Atoi(head); err == nil && strings.Contains(f, ".") {
			return n
		}
	}
	return 0
}
