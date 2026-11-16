package render

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/doneill612/vmono/internal/manifest"
)

// claudeSettingsPath is the generated project settings file.
const claudeSettingsPath = ".claude/settings.json"

// vmonoClaudeRules are the rules the root settings always carry: reference
// members are never edited, and vmono's destructive commands ask first.
var vmonoClaudeRules = []struct {
	rule   string
	effect Effect
}{
	{"Edit(/_ref/**)", EffectDeny},
	{"Bash(vmono rm:*)", EffectAsk},
	{"Bash(vmono delete:*)", EffectAsk},
	{"Bash(vmono session rm:*)", EffectAsk},
}

// claudeFile is the subset of a Claude Code settings.json vmono reads and
// writes.
type claudeFile struct {
	Permissions claudePermissions           `json:"permissions"`
	Hooks       map[string][]map[string]any `json:"hooks,omitempty"`
}

// claudePermissions holds the three rule lists.
type claudePermissions struct {
	Allow []string `json:"allow"`
	Ask   []string `json:"ask"`
	Deny  []string `json:"deny"`
}

// renderClaudeCode writes .claude/settings.json at the package root and
// brings member skills up to it. The settings merge each member's
// permission rules (re-anchored under the member's folder, stricter wins)
// and, for members with hooks = true, their hooks wrapped to run from the
// member's directory. vmono's own rules are always kept.
//
// A member's unreadable or malformed settings file is skipped rather than
// failing the whole render.
func renderClaudeCode(w *writer, m *manifest.Manifest) error {
	rules := newRuleSet()
	for _, r := range vmonoClaudeRules {
		rules.add(r.rule, r.effect)
	}
	hooks := map[string][]map[string]any{}

	for _, mem := range m.Members {
		cfg, ok := readClaudeMember(filepath.Join(w.dir, filepath.FromSlash(mem.Path)))
		if !ok {
			continue
		}
		for _, group := range []struct {
			effect Effect
			list   []string
		}{
			{EffectAllow, cfg.Permissions.Allow},
			{EffectAsk, cfg.Permissions.Ask},
			{EffectDeny, cfg.Permissions.Deny},
		} {
			for _, rule := range group.list {
				if rewritten, keep := RewriteRule(mem.Path, rule, group.effect); keep {
					rules.add(rewritten, group.effect)
				}
			}
		}
		if mem.Hooks {
			mergeClaudeHooks(hooks, cfg.Hooks, mem.Path)
		}
	}

	out := claudeFile{
		Permissions: claudePermissions{
			Allow: rules.list(EffectAllow),
			Ask:   rules.list(EffectAsk),
			Deny:  rules.list(EffectDeny),
		},
	}
	if len(hooks) > 0 {
		out.Hooks = hooks
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := w.file(claudeSettingsPath, string(data)+"\n"); err != nil {
		return err
	}
	if err := writeBundledSkill(w, ".claude/skills"); err != nil {
		return err
	}
	return syncMemberSkills(w, m, skillRoot{dest: ".claude/skills", sources: []string{".claude/skills"}})
}

// readClaudeMember reads a member's .claude/settings.json.
func readClaudeMember(memberDir string) (claudeFile, bool) {
	data, err := os.ReadFile(filepath.Join(memberDir, ".claude", "settings.json"))
	if err != nil {
		return claudeFile{}, false
	}
	var f claudeFile
	if json.Unmarshal(data, &f) != nil {
		return claudeFile{}, false
	}
	return f, true
}

// mergeClaudeHooks appends a member's hooks to dst, wrapping each command
// to run from the member's directory. Hooks fire for the whole session, not
// just for the member's files, which is why they are opt-in.
func mergeClaudeHooks(dst, src map[string][]map[string]any, memberPath string) {
	for event, matchers := range src {
		for _, matcher := range matchers {
			clone := map[string]any{}
			for k, v := range matcher {
				clone[k] = v
			}
			if inner, ok := matcher["hooks"].([]any); ok {
				wrapped := make([]any, 0, len(inner))
				for _, h := range inner {
					hm, ok := h.(map[string]any)
					if !ok {
						continue
					}
					hc := map[string]any{}
					for k, v := range hm {
						hc[k] = v
					}
					if cmd, ok := hm["command"].(string); ok {
						hc["command"] = fmt.Sprintf(`cd "$CLAUDE_PROJECT_DIR/%s" && %s`, strings.ReplaceAll(memberPath, `\`, "/"), cmd)
					}
					wrapped = append(wrapped, hc)
				}
				clone["hooks"] = wrapped
			}
			dst[event] = append(dst[event], clone)
		}
	}
}
