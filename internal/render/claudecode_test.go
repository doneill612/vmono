package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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
	m.ApplyDefaults()
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
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	f := readClaudeSettings(t, dir)

	for _, want := range []string{"Edit(/_ref/**)", "Read(/api/**/.env)"} {
		if !contains(f.Permissions.Deny, want) {
			t.Errorf("deny missing %q: %v", want, f.Permissions.Deny)
		}
	}
	for _, want := range []string{"Bash(vmono rm:*)", "Bash(vmono delete:*)", "Bash(vmono session rm:*)", "Bash(vmono session finish:*)", "Bash(npm test:*)"} {
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
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
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
	if _, err := Render(dir, m, testSession(m), opts); err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join(dir, ".claude/skills/api-deploy/SKILL.md"))
	if err != nil || !strings.Contains(string(skill), "name: api-deploy") || !strings.Contains(string(skill), "description: ship it") {
		t.Fatalf("copied skill wrong: %v\n%s", err, skill)
	}
	// Windows has no executable permission bit; Go reports 0666 for every file.
	if info, err := os.Stat(filepath.Join(dir, ".claude/skills/api-deploy/run.sh")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0) {
		t.Errorf("script must stay executable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/sdk-lint/SKILL.md")); err != nil {
		t.Error("sdk skill missing")
	}

	if changed, err := Render(dir, m, testSession(m), opts); err != nil || len(changed) != 0 {
		t.Errorf("second render should be a no-op: %v, %v", changed, err)
	}

	// Removing a member's skill prunes only the marked copy.
	if err := os.RemoveAll(filepath.Join(dir, "sdk/.claude/skills/lint")); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(dir, m, testSession(m), opts); err != nil {
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
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode, AdapterOpenCode}}); err != nil {
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
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/vmono/SKILL.md")); err != nil {
		t.Error("bundled skill pruned")
	}
	// Without adapters no harness config is written.
	bare := t.TempDir()
	if _, err := Render(bare, m, testSession(m), Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(bare, ".claude")); !os.IsNotExist(err) {
		t.Error("no adapter, no .claude directory")
	}
}

func TestClaudePluginWrittenAndKeptExact(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	opts := Options{Adapters: []string{AdapterClaudeCode}}
	if _, err := Render(dir, m, testSession(m), opts); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(data)
	}

	var manifest struct {
		Name   string `json:"name"`
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if err := json.Unmarshal([]byte(read(".claude/skills/vmono/.claude-plugin/plugin.json")), &manifest); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if manifest.Name != "vmono" || manifest.Author.Name == "" {
		t.Errorf("plugin.json = %+v", manifest)
	}
	if got := read(".claude/skills/vmono/SKILL.md"); got != SkillMarkdown() {
		t.Error("the plugin's root SKILL.md must be the bundled skill, so a blocked plugin falls back to it")
	}

	// Anything else under the plugin directory is vmono's to remove, so a
	// file from an older version cannot keep loading.
	stray := filepath.Join(dir, ".claude/skills/vmono/old/nested/leftover.md")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Render(dir, m, testSession(m), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/vmono/old")); !os.IsNotExist(err) {
		t.Error("stray file and the directories it emptied should be removed")
	}
	if len(changed) != 1 || changed[0] != ".claude/skills/vmono/old/nested/leftover.md" {
		t.Errorf("only the stray file should be reported, got %v", changed)
	}

	// With nothing stale, a render leaves the plugin untouched.
	changed, err = Render(dir, m, testSession(m), opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range changed {
		if strings.HasPrefix(rel, ".claude/skills/vmono/") {
			t.Errorf("unchanged plugin rewritten: %s", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude/skills/mine/SKILL.md")); err != nil {
		t.Error("the user's own skill must survive")
	}
}

func TestClaudePluginCommands(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, ".claude/skills/vmono/skills", name, "SKILL.md"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return string(data)
	}

	finish := read("finish")
	for _, want := range []string{"name: finish\n", "disable-model-invocation: true\n", finishSection, "vmono session finish --json"} {
		if !strings.Contains(finish, want) {
			t.Errorf("finish missing %q", want)
		}
	}
	// A pre-approval here would silence the adapter's ask rule for
	// `vmono session finish`.
	if strings.Contains(finish, "allowed-tools") {
		t.Error("finish must not pre-approve tools: the ask rule for vmono session finish has to keep prompting")
	}

	note := read("note")
	for _, want := range []string{"name: note\n", `argument-hint: "[note text]"`, `allowed-tools: "Bash(vmono note:*)"`, "$ARGUMENTS", whatGoesWhere} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q", want)
		}
	}

	status := read("status")
	for _, want := range []string{"name: status\n", `allowed-tools: "Bash(vmono status:*)"`, "vmono status --json"} {
		if !strings.Contains(status, want) {
			t.Errorf("status missing %q", want)
		}
	}
	if strings.Contains(status, "disable-model-invocation") {
		t.Error("status is read-only and may be run by Claude")
	}
	for name, body := range map[string]string{"finish": finish, "note": note, "status": status} {
		if !strings.HasPrefix(body, "---\nname: "+name+"\n") || !strings.Contains(body, "\n---\n<!-- "+GeneratedNote+" -->") {
			t.Errorf("%s: frontmatter malformed:\n%s", name, body)
		}
	}
}

func TestClaudePluginSessionStartHook(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterClaudeCode}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude/skills/vmono/hooks/hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Timeout int      `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("hooks.json: %v\n%s", err, data)
	}
	groups := f.Hooks["SessionStart"]
	if len(f.Hooks) != 1 || len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("want exactly one SessionStart hook, got %s", data)
	}
	if groups[0].Matcher != "" {
		t.Errorf("no matcher, so it runs on startup, resume, clear, fork and compact: %q", groups[0].Matcher)
	}
	h := groups[0].Hooks[0]
	// Exec form: args present, command a bare executable with no shell syntax.
	if h.Type != "command" || h.Command != "vmono" || strings.Join(h.Args, " ") != "hook session-start" || h.Timeout <= 0 {
		t.Errorf("hook = %+v", h)
	}
}
