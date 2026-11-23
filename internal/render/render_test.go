package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/manifest"
)

func sample() *manifest.Manifest {
	m := &manifest.Manifest{
		Name:    "platform-auth",
		Created: time.Now(),
		Purpose: "Move token refresh into the gateway.",
		Context: "Ticket PLAT-412.",
		Members: []manifest.Member{
			{Name: "api-gateway", Source: "/s/api", Mode: manifest.ModeActive, Role: "Owns | refresh\nendpoint."},
			{Name: "contracts", Source: "/s/c", Mode: manifest.ModeReference, Role: "Specs."},
		},
	}
	m.ApplyDefaults()
	return m
}

// testSession returns the session the render tests render a package into.
func testSession(m *manifest.Manifest) *manifest.Session {
	s := &manifest.Session{Package: m.Name, Name: "work", Created: time.Now()}
	s.ApplyDefaults("vmono/")
	return s
}

func TestAgentsBlockContents(t *testing.T) {
	got := AgentsBlock(sample(), testSession(sample()))
	for _, want := range []string{
		BeginMarker, EndMarker, "# Package: platform-auth · session work", "Move token refresh",
		"## Context", "Ticket PLAT-412.", "| `api-gateway/` | active | `vmono/platform-auth/work` |",
		`Owns \| refresh endpoint.`, "| `_ref/contracts/` | reference | `origin/HEAD` |", "Never edit anything under `_ref/`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("block missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Notes") {
		t.Error("no notes section expected without notes")
	}
}

func TestNotesNewestFirstAndCapped(t *testing.T) {
	m := sample()
	for i := 0; i < 25; i++ {
		m.Notes = append(m.Notes, manifest.Note{At: time.Date(2026, 11, 1, 0, i, 0, 0, time.UTC), By: "user", Text: "note"})
	}
	got := AgentsBlock(m, testSession(m))
	if n := strings.Count(got, "- 2026-11-01"); n != manifest.MaxNotesRendered {
		t.Errorf("rendered %d notes, want %d", n, manifest.MaxNotesRendered)
	}
	if strings.Index(got, "00:24") > strings.Index(got, "00:23") {
		t.Error("newest note must come first")
	}
}

func TestMergeAgents(t *testing.T) {
	block := AgentsBlock(sample(), testSession(sample()))

	if got, err := MergeAgents("", block); err != nil || got != block {
		t.Errorf("empty merge = %v", err)
	}

	user := "# My notes\n\nkeep me\n"
	got, err := MergeAgents(user, block)
	if err != nil || !strings.HasPrefix(got, BeginMarker) || !strings.HasSuffix(got, user) {
		t.Errorf("markerless merge should prepend block and keep text: %v\n%s", err, got)
	}

	stale := "above\n" + BeginMarker + "\nOLD\n" + EndMarker + "\nbelow\n"
	got, err = MergeAgents(stale, block)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "OLD") || !strings.HasPrefix(got, "above\n") || !strings.HasSuffix(got, "below\n") {
		t.Errorf("replace lost user text or kept old block:\n%s", got)
	}
	again, _ := MergeAgents(got, block)
	if again != got {
		t.Error("merge must be idempotent")
	}

	for _, bad := range []string{BeginMarker + "\nno end", EndMarker + "\n" + BeginMarker} {
		if _, err := MergeAgents(bad, block); err == nil {
			t.Errorf("unbalanced markers %q should error", bad)
		}
	}
}

func TestRenderWritesOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	m := sample()
	changed, err := Render(dir, m, testSession(m), Options{})
	if err != nil || len(changed) != 3 {
		t.Fatalf("first render = %v, %v", changed, err)
	}
	claude, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(claude) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q", claude)
	}
	ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(ign), "/api-gateway/\n") || !strings.Contains(string(ign), "/_ref/\n") || strings.Count(string(ign), "/_ref/") != 1 {
		t.Errorf(".gitignore = %q", ign)
	}

	if changed, err = Render(dir, m, testSession(m), Options{}); err != nil || len(changed) != 0 {
		t.Errorf("second render should change nothing: %v, %v", changed, err)
	}

	agents := filepath.Join(dir, "AGENTS.md")
	data, _ := os.ReadFile(agents)
	if err := os.WriteFile(agents, append(data, []byte("\nUSER TEXT\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Purpose = "new purpose"
	if changed, err = Render(dir, m, testSession(m), Options{}); err != nil || len(changed) != 1 || changed[0] != "AGENTS.md" {
		t.Fatalf("purpose change should rewrite only AGENTS.md: %v, %v", changed, err)
	}
	data, _ = os.ReadFile(agents)
	if !strings.Contains(string(data), "USER TEXT") || !strings.Contains(string(data), "new purpose") {
		t.Errorf("user text lost or purpose not updated:\n%s", data)
	}
}

func TestRenderPackageWritesOnlyTheGitignore(t *testing.T) {
	dir := t.TempDir()
	changed, err := RenderPackage(dir)
	if err != nil || len(changed) != 1 || changed[0] != ".gitignore" {
		t.Fatalf("RenderPackage = %v, %v", changed, err)
	}
	ign, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(ign), "/*/\n") || !strings.Contains(string(ign), "!/.vmono/\n") {
		t.Errorf("package .gitignore = %q", ign)
	}
	if changed, err := RenderPackage(dir); err != nil || len(changed) != 0 {
		t.Errorf("second RenderPackage should change nothing: %v, %v", changed, err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s must not be written in a package directory", name)
		}
	}
}

func TestSkillTeachesFinishing(t *testing.T) {
	body := SkillMarkdown()
	for _, want := range []string{"vmono session finish --json", "--dry-run", "git -c core.editor=true rebase --continue", "Never pass `--close`", "Never push", "vmono session into", "--member NAME=BRANCH"} {
		if !strings.Contains(body, want) {
			t.Errorf("skill missing %q", want)
		}
	}
}

func TestSkillExplainsSessions(t *testing.T) {
	body := SkillMarkdown()
	for _, want := range []string{"VMONO_SESSION", "session", "every session", "vmono session rm"} {
		if !strings.Contains(body, want) {
			t.Errorf("skill missing %q", want)
		}
	}
}

// TestSkillCarriesWhatThePluginCommandsQuote pins that the guidance behind
// /vmono:status and /vmono:note is in the skill every harness gets, so a
// harness without the plugin is not left with less.
func TestSkillCarriesWhatThePluginCommandsQuote(t *testing.T) {
	body := SkillMarkdown()
	for _, want := range []string{
		"vmono status --json",
		"a rebase in progress",
		"show them the text first",
		"record nothing rather",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill missing %q", want)
		}
	}
}
