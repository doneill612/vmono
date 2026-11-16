package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteOpenCodeResource(t *testing.T) {
	cases := []struct {
		action, res string
		effect      Effect
		want        string
		keep        bool
	}{
		{"edit", "src/**", EffectAllow, "api/src/**", true},
		{"edit", "./src/**", EffectDeny, "api/src/**", true},
		{"edit", "*", EffectAllow, "api/*", true},
		{"bash", "npm test *", EffectAllow, "npm test *", true},
		{"bash", "*", EffectAllow, "", false},
		{"bash", "*", EffectDeny, "*", true},
	}
	for _, c := range cases {
		got, keep := RewriteOpenCodeResource("api", c.action, c.res, c.effect)
		if got != c.want || keep != c.keep {
			t.Errorf("%s %q %v = %q,%v want %q,%v", c.action, c.res, c.effect, got, keep, c.want, c.keep)
		}
	}
}

func TestParseMajor(t *testing.T) {
	for in, want := range map[string]int{"opencode 2.0.1": 2, "v1.4.0\n": 1, "3.1.0": 3, "nonsense": 0, "": 0} {
		if got := parseMajor(in); got != want {
			t.Errorf("parseMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

// ocPackage lays out a package whose api member has an opencode.json.
func ocPackage(t *testing.T, memberConfig string) string {
	t.Helper()
	dir, m := pkgWithMembers(t, false)
	_ = m
	if memberConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "api", "opencode.json"), []byte(memberConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOpenCodeV1Output(t *testing.T) {
	dir := ocPackage(t, `{"permission": {"edit": {"*": "allow", "secrets/**": "deny"}, "bash": {"npm test *": "allow", "*": "allow"}, "webfetch": "allow"}}`)
	_, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterOpenCode}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	var doc struct {
		Schema     string                       `json:"$schema"`
		Permission map[string]map[string]string `json:"permission"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, text)
	}
	if doc.Schema == "" || doc.Permission["edit"]["_ref/**"] != "deny" {
		t.Errorf("vmono _ref deny missing:\n%s", text)
	}
	if doc.Permission["edit"]["api/secrets/**"] != "deny" || doc.Permission["edit"]["api/*"] != "allow" {
		t.Errorf("member edit rules not re-anchored:\n%s", text)
	}
	if doc.Permission["bash"]["vmono rm *"] != "ask" || doc.Permission["bash"]["vmono delete *"] != "ask" || doc.Permission["bash"]["vmono session rm *"] != "ask" {
		t.Errorf("vmono bash asks missing:\n%s", text)
	}
	if doc.Permission["bash"]["npm test *"] != "allow" {
		t.Errorf("member bash rule lost:\n%s", text)
	}
	if _, widened := doc.Permission["bash"]["*"]; widened {
		t.Errorf("member's catch-all bash allow must be dropped:\n%s", text)
	}
	if _, ok := doc.Permission["webfetch"]; ok {
		t.Error("unmerged actions must be left to the user's config")
	}
	// Last match wins: vmono's deny must come after every member edit rule.
	if strings.Index(text, `"_ref/**"`) < strings.Index(text, `"api/secrets/**"`) {
		t.Errorf("vmono rules must be last:\n%s", text)
	}
}

func TestOpenCodeV2Output(t *testing.T) {
	dir := ocPackage(t, `{"permissions": [{"action": "shell", "resource": "make *", "effect": "allow"}, {"action": "edit", "resource": "gen/**", "effect": "deny"}]}`)
	_, m := pkgWithMembers(t, false)
	if _, err := Render(dir, m, testSession(m), Options{Adapters: []string{AdapterOpenCode}, OpenCodeV2: true}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "opencode.json"))
	var doc struct {
		Permissions []struct{ Action, Resource, Effect string } `json:"permissions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, data)
	}
	idx := func(action, resource string) int {
		for i, p := range doc.Permissions {
			if p.Action == action && p.Resource == resource {
				return i
			}
		}
		return -1
	}
	if idx("shell", "make *") < 0 || idx("edit", "api/gen/**") < 0 {
		t.Errorf("member rules not merged/re-anchored:\n%s", data)
	}
	if idx("edit", "_ref/**") < idx("edit", "api/gen/**") || idx("shell", "vmono rm *") < 0 {
		t.Errorf("vmono rules missing or not last:\n%s", data)
	}
	var sawAbs bool
	for _, p := range doc.Permissions {
		if p.Action == "edit" && strings.HasSuffix(p.Resource, "/_ref/**") && filepath.IsAbs(filepath.FromSlash(p.Resource)) {
			sawAbs = true
		}
	}
	if !sawAbs {
		t.Errorf("v2 should also deny the absolute _ref path:\n%s", data)
	}
	if strings.Contains(string(data), `"bash"`) {
		t.Error("v2 must use shell, not bash")
	}
}

func TestOpenCodeSkillsAndIdempotence(t *testing.T) {
	dir, m := pkgWithMembers(t, false)
	opts := Options{Adapters: []string{AdapterOpenCode}}
	if _, err := Render(dir, m, testSession(m), opts); err != nil {
		t.Fatal(err)
	}
	// OpenCode copies draw from member .claude/skills too.
	if _, err := os.Stat(filepath.Join(dir, ".opencode/skills/api-deploy/SKILL.md")); err != nil {
		t.Errorf("skill not copied for opencode: %v", err)
	}
	if changed, err := Render(dir, m, testSession(m), opts); err != nil || len(changed) != 0 {
		t.Errorf("second render should be a no-op: %v %v", changed, err)
	}
}
