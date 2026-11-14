package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/manifest"
)

func TestRewritePathPattern(t *testing.T) {
	cases := map[string]string{
		"src/**":      "/api/src/**",
		"./src/**":    "/api/src/**",
		"/src/**":     "/api/src/**",
		".env":        "/api/**/.env",
		"*.env":       "/api/**/*.env",
		"**/secret/*": "/api/**/secret/*",
		"//etc/hosts": "//etc/hosts",
		"~/notes/**":  "~/notes/**",
		"**":          "/api/**",
	}
	for in, want := range cases {
		if got := RewritePathPattern("api", in); got != want {
			t.Errorf("RewritePathPattern(%q) = %q, want %q", in, got, want)
		}
	}
	if got := RewritePathPattern("services/api", "src/**"); got != "/services/api/src/**" {
		t.Errorf("nested member path: %q", got)
	}
}

func TestRewriteRule(t *testing.T) {
	cases := []struct {
		rule   string
		effect Effect
		want   string
		keep   bool
	}{
		{"Edit(src/**)", EffectAllow, "Edit(/api/src/**)", true},
		{"Read(.env)", EffectDeny, "Read(/api/**/.env)", true},
		{"Bash(npm run test:*)", EffectAllow, "Bash(npm run test:*)", true},
		{"WebFetch(domain:example.com)", EffectAllow, "WebFetch(domain:example.com)", true},
		{"Edit", EffectAllow, "", false},
		{"Edit", EffectDeny, "Edit(/api/**)", true},
		{"Bash", EffectAllow, "Bash", true},
	}
	for _, c := range cases {
		got, keep := RewriteRule("api", c.rule, c.effect)
		if got != c.want || keep != c.keep {
			t.Errorf("RewriteRule(%q, %v) = %q, %v; want %q, %v", c.rule, c.effect, got, keep, c.want, c.keep)
		}
	}
}

// pkgWithMembers lays out a package directory with member config files.
func pkgWithMembers(t *testing.T, hooks bool) (string, *manifest.Manifest) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("api/.claude/settings.json", `{
  "permissions": {
    "allow": ["Edit(src/**)", "Bash(npm test:*)", "Edit", "Bash(vmono rm:*)"],
    "deny": ["Read(.env)"]
  },
  "hooks": {"PostToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "npm run lint"}]}]}
}`)
	write("api/.claude/skills/deploy/SKILL.md", "---\nname: deploy\ndescription: ship it\n---\nbody\n")
	write("api/.claude/skills/deploy/run.sh", "#!/bin/sh\n")
	write("sdk/.claude/settings.json", `{"permissions": {"ask": ["Bash(npm test:*)"]}}`)
	write("sdk/.claude/skills/lint/SKILL.md", "---\nname: lint\n---\n")
	write(".claude/skills/mine/SKILL.md", "---\nname: mine\n---\nuser's own skill\n")

	m := &manifest.Manifest{Name: "p", Created: time.Now(), Members: []manifest.Member{
		{Name: "api", Source: "/s/api", Mode: manifest.ModeActive, Hooks: hooks},
		{Name: "sdk", Source: "/s/sdk", Mode: manifest.ModeActive},
		{Name: "ref", Source: "/s/ref", Mode: manifest.ModeReference},
	}}
	m.ApplyDefaults("vmono/")
	return dir, m
}

func readClaudeSettings(t *testing.T, dir string) claudeFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f claudeFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, data)
	}
	return f
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestClaudeAdapterMergesMemberPermissions(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	f := readClaudeSettings(t, dir)

	for _, want := range []string{"Edit(/_ref/**)", "Read(/api/**/.env)"} {
		if !contains(f.Permissions.Deny, want) {
			t.Errorf("deny missing %q: %v", want, f.Permissions.Deny)
		}
	}
	for _, want := range []string{"Bash(vmono rm:*)", "Bash(vmono delete:*)", "Bash(npm test:*)"} {
		if !contains(f.Permissions.Ask, want) {
			t.Errorf("ask missing %q: %v", want, f.Permissions.Ask)
		}
	}
	// sdk asks for npm test while api allows it: stricter wins, so it is
	// not also in allow. api's allow of vmono rm must not weaken vmono's ask.
	if contains(f.Permissions.Allow, "Bash(npm test:*)") || contains(f.Permissions.Allow, "Bash(vmono rm:*)") {
		t.Errorf("stricter effect must win: allow=%v", f.Permissions.Allow)
	}
	if !contains(f.Permissions.Allow, "Edit(/api/src/**)") {
		t.Errorf("rewritten allow missing: %v", f.Permissions.Allow)
	}
	if contains(f.Permissions.Allow, "Edit") {
		t.Error("a bare Edit allow from a member would widen access and must be dropped")
	}
	if f.Hooks != nil {
		t.Error("hooks must be off unless the member opts in")
	}
}

func TestClaudeAdapterHooksOptIn(t *testing.T) {
	dir, m := pkgWithMembers(t, true)
	if _, err := Render(dir, m, Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	f := readClaudeSettings(t, dir)
	entries := f.Hooks["PostToolUse"]
	if len(entries) != 1 {
		t.Fatalf("hooks = %+v", f.Hooks)
	}
	inner := entries[0]["hooks"].([]any)[0].(map[string]any)
	if got := inner["command"].(string); got != `cd "$CLAUDE_PROJECT_DIR/api" && npm run lint` {
		t.Errorf("hook command = %q", got)
	}
	if entries[0]["matcher"] != "Edit" || inner["type"] != "command" {
		t.Errorf("hook fields lost: %+v", entries[0])
	}
}

func TestClaudeAdapterSkillsCopiedRenamedAndPruned(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	opts := Options{Adapters: []string{AdapterClaudeCode}}
	if _, err := Render(dir, m, opts); err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join(dir, ".claude/skills/api-deploy/SKILL.md"))
	if err != nil || !strings.Contains(string(skill), "name: api-deploy") || !strings.Contains(string(skill), "description: ship it") {
		t.Fatalf("copied skill wrong: %v\n%s", err, skill)
	}
	if info, err := os.Stat(filepath.Join(dir, ".claude/skills/api-deploy/run.sh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("script must stay executable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/sdk-lint/SKILL.md")); err != nil {
		t.Error("sdk skill missing")
	}

	if changed, err := Render(dir, m, opts); err != nil || len(changed) != 0 {
		t.Errorf("second render should be a no-op: %v, %v", changed, err)
	}

	// Removing a member's skill prunes only the marked copy.
	if err := os.RemoveAll(filepath.Join(dir, "sdk/.claude/skills/lint")); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(dir, m, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/sdk-lint")); !os.IsNotExist(err) {
		t.Error("stale copied skill should be removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/mine/SKILL.md")); err != nil {
		t.Error("user's own skill must survive")
	}
}

func TestRewriteSkillName(t *testing.T) {
	got := RewriteSkillName("---\nname: old\ndescription: name: keep\n---\nname: body stays\n", "new")
	if !strings.Contains(got, "name: new\n") || !strings.Contains(got, "name: body stays") || !strings.Contains(got, "description: name: keep") {
		t.Errorf("RewriteSkillName = %q", got)
	}
	if plain := "no frontmatter\nname: x\n"; RewriteSkillName(plain, "new") != plain {
		t.Error("text without frontmatter must be unchanged")
	}
}

func TestBundledSkillWrittenForBothAdapters(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, Options{Adapters: []string{AdapterClaudeCode, AdapterOpenCode}}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".claude/skills/vmono/SKILL.md", ".opencode/skills/vmono/SKILL.md"} {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		text := string(data)
		for _, want := range []string{"name: vmono", "vmono show --json", "vmono note", "vmono describe --context", "vmono role", "vmono add", "Never remove a member", "VMONO_PACKAGE"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", rel, want)
			}
		}
	}
	// The bundled skill is not a member copy and must survive pruning.
	if _, err := Render(dir, m, Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/vmono/SKILL.md")); err != nil {
		t.Error("bundled skill pruned")
	}
	// Without adapters no harness config is written.
	bare := t.TempDir()
	if _, err := Render(bare, m, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bare, ".claude")); !os.IsNotExist(err) {
		t.Error("no adapter, no .claude directory")
	}
}
