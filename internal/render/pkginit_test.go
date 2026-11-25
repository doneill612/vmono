package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/manifest"
)

// initManifest is a package as an init workspace holds it.
func initManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Schema: manifest.SchemaVersion, Name: "demo", Purpose: "why", Context: "known facts",
		Members: []manifest.Member{{Name: "alpha", Source: "/a", Mode: manifest.ModeReference, Path: "_ref/alpha", Ref: "origin/main", Hooks: true}},
	}
}

// writeIn writes a file of a member in dir.
func writeIn(t *testing.T, dir, rel, text string) {
	t.Helper()
	file := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readIn(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRenderInitRestrictsEveryAdapter(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, dir, "_ref/alpha/.claude/settings.json", `{"permissions":{"allow":["Bash(npm:*)"],"ask":["Bash(make:*)"],"deny":["Read(/secrets/**)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]}}`)
	writeIn(t, dir, "_ref/alpha/.claude/skills/own/SKILL.md", "---\nname: own\n---\n")
	writeIn(t, dir, "_ref/alpha/opencode.json", `{"permission":{"edit":{"*":"ask"},"bash":{"npm *":"allow","rm *":"deny"}}}`)
	opts := Options{Adapters: []string{AdapterClaudeCode, AdapterOpenCode, AdapterAgentSkills}}
	if err := RenderInit(dir, initManifest(), opts); err != nil {
		t.Fatal(err)
	}

	var cf claudeFile
	if err := json.Unmarshal([]byte(readIn(t, dir, ".claude/settings.json")), &cf); err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"Edit", "Write", "NotebookEdit", "Bash(git push:*)", "Bash(vmono package suggestions:*)", "Read(/_ref/alpha/secrets/**)"} {
		if !slices.Contains(cf.Permissions.Deny, r) {
			t.Errorf("deny lacks %s: %v", r, cf.Permissions.Deny)
		}
	}
	if !slices.Contains(cf.Permissions.Allow, "Bash(vmono package record:*)") || slices.Contains(cf.Permissions.Allow, "Bash(npm:*)") {
		t.Errorf("allow = %v", cf.Permissions.Allow)
	}
	if !slices.Contains(cf.Permissions.Ask, "Bash(make:*)") || len(cf.Hooks) != 0 {
		t.Errorf("ask = %v hooks = %v", cf.Permissions.Ask, cf.Hooks)
	}

	oc := strings.Join(strings.Fields(readIn(t, dir, "opencode.json")), "")
	if !strings.Contains(oc, `"edit":{"*":"deny"}`) || strings.Contains(oc, "npm") {
		t.Errorf("opencode.json = %s", oc)
	}
	order := []string{`"*":"ask"`, `"rm*":"deny"`, `"vmonopackagerecord*":"allow"`}
	last := -1
	for _, rule := range order {
		at := strings.Index(oc, rule)
		if at <= last {
			t.Errorf("rules out of order at %s: %s", rule, oc)
		}
		last = at
	}
	if !strings.Contains(readIn(t, dir, codexRulesPath), `["vmono", "package", "record"]`) {
		t.Error("codex rules should allow recording")
	}
	for _, rel := range []string{".claude/skills", ".opencode", ".agents"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("%s should not be written", rel)
		}
	}

	if err := RenderInit(dir, initManifest(), Options{Adapters: []string{AdapterOpenCode}, OpenCodeV2: true}); err != nil {
		t.Fatal(err)
	}
	if v2 := readIn(t, dir, "opencode.json"); !strings.Contains(v2, `"action": "shell"`) {
		t.Errorf("v2 config = %s", v2)
	}
}

func TestInitBlockCarriesThePackageAndDrift(t *testing.T) {
	m := initManifest()
	first := InitBlock(m)
	for _, want := range []string{"vmono package record -", "known facts", "`_ref/alpha/`", "origin/main"} {
		if !strings.Contains(first, want) {
			t.Errorf("block lacks %q", want)
		}
	}
	if strings.Contains(first, "recorded earlier") {
		t.Error("a first init has no earlier overview")
	}
	m.Init = &manifest.Init{Overview: "old", Members: []string{"gone"}}
	again := InitBlock(m)
	for _, want := range []string{"old", "added since: alpha", "removed since: gone"} {
		if !strings.Contains(again, want) {
			t.Errorf("rerun block lacks %q", want)
		}
	}
}
