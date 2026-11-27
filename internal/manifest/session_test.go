package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleSession() *Session {
	s := &Session{
		Package: "platform-auth",
		Name:    "token-refresh",
		Created: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC),
	}
	s.ApplyDefaults()
	return s
}

func TestSessionDefaultBranch(t *testing.T) {
	if s := sampleSession(); s.Branch != "token-refresh" {
		t.Errorf("branch = %q", s.Branch)
	}
	s := &Session{Package: "p", Name: "s", Branch: "feat/PLAT-412"}
	s.ApplyDefaults()
	if s.Branch != "feat/PLAT-412" {
		t.Errorf("explicit branch overwritten: %q", s.Branch)
	}
	s = &Session{Schema: LandingSessionSchema, Package: "p", Name: "s"}
	s.ApplyDefaults()
	if s.Branch != "vmono/p/s" {
		t.Errorf("a file from before keeps the default of its time: %q", s.Branch)
	}
	for prefix, want := range map[string]string{"": "s", "feat": "feat/s", "feat/": "feat/s", "/": "s", "me/feat": "me/feat/s"} {
		if got := DefaultBranch(prefix, "s"); got != want {
			t.Errorf("prefix %q: %q, want %q", prefix, got, want)
		}
	}
}

func TestSessionEncodeRoundTrip(t *testing.T) {
	s := sampleSession()
	got, err := ParseSession(EncodeSession(s))
	if err != nil {
		t.Fatalf("parse of encoded session: %v\n%s", err, EncodeSession(s))
	}
	if got.Package != s.Package || got.Name != s.Name || got.Branch != s.Branch || !got.Created.Equal(s.Created) {
		t.Errorf("round trip mismatch: %+v vs %+v", got, s)
	}
}

func TestSessionValidate(t *testing.T) {
	cases := map[string]func(*Session){
		"spaced name":   func(s *Session) { s.Name = "bad name" },
		"leading mark":  func(s *Session) { s.Name = "-bad" },
		"trailing mark": func(s *Session) { s.Name = "bad_" },
		"dotted name":   func(s *Session) { s.Name = ".vmono" },
		"bad package":   func(s *Session) { s.Package = "" },
		"empty branch":  func(s *Session) { s.Branch = " " },
		"wrong schema":  func(s *Session) { s.Schema = 9 },
		"slash in name": func(s *Session) { s.Name = "a/b" },
	}
	for name, mutate := range cases {
		s := sampleSession()
		mutate(s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if err := sampleSession().Validate(); err != nil {
		t.Errorf("valid session rejected: %v", err)
	}
	for _, name := range []string{"PLAT-412", "user_auth", "a--b__c"} {
		s := sampleSession()
		s.Name = name
		if err := s.Validate(); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}

func TestParseSessionRejectsUnknownKeys(t *testing.T) {
	body := string(EncodeSession(sampleSession())) + "bogus = 1\n"
	if _, err := ParseSession([]byte(body)); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("expected unknown key error, got %v", err)
	}
}

func TestSaveLoadAndFindSession(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSession(dir, sampleSession()); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(dir); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "api-gateway", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindSession(deep)
	if err != nil || got != dir {
		t.Errorf("FindSession = %q, %v; want %q", got, err, dir)
	}
	t.Setenv("VMONO_ROOT", dir)
	if got, err := FindSession(t.TempDir()); err != nil || got != dir {
		t.Errorf("FindSession via VMONO_ROOT = %q, %v", got, err)
	}
	t.Setenv("VMONO_ROOT", "")
	if _, err := FindSession(t.TempDir()); err == nil {
		t.Error("expected not-found outside a session")
	}
}

func TestSessionIntoAndFinishedRoundTrip(t *testing.T) {
	s := sampleSession()
	s.Into = map[string]string{"sdk-go": "develop", "api-gateway": "feature/PLAT-412"}
	s.Finished = time.Date(2026, 11, 17, 15, 4, 5, 0, time.UTC)
	body := string(EncodeSession(s))
	if strings.Index(body, "finished") > strings.Index(body, "[into]") {
		t.Errorf("scalars must come before the [into] table or they would belong to it:\n%s", body)
	}
	if strings.Index(body, "api-gateway") > strings.Index(body, "sdk-go") {
		t.Errorf("members should be written in a stable order:\n%s", body)
	}
	got, err := ParseSession([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	if got.Into["api-gateway"] != "feature/PLAT-412" || got.Into["sdk-go"] != "develop" || len(got.Into) != 2 || !got.Finished.Equal(s.Finished) {
		t.Errorf("round trip mismatch: into=%v finished=%v", got.Into, got.Finished)
	}

	plain, err := ParseSession(EncodeSession(sampleSession()))
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Into) != 0 || !plain.Finished.IsZero() {
		t.Errorf("unset fields must stay unset: %+v", plain)
	}
	body = string(EncodeSession(sampleSession()))
	if strings.Contains(body, "into") || strings.Contains(body, "finished") {
		t.Errorf("unset fields must not be written:\n%s", body)
	}
}

func TestSessionFileWithoutNewFieldsStillLoads(t *testing.T) {
	old := "schema  = 1\npackage = \"p\"\nname    = \"s\"\ncreated = 2026-11-16T09:00:00Z\nbranch  = \"vmono/p/s\"\n"
	s, err := ParseSession([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Into) != 0 || !s.Finished.IsZero() {
		t.Errorf("old file gained values: %+v", s)
	}
}

func TestSessionValidateInto(t *testing.T) {
	for _, bad := range []string{"-x", "a b", "a..b", "/x", "x/", "x.lock", "a~1", "a:b", "token-refresh", ""} {
		s := sampleSession()
		s.Into = map[string]string{"api": bad}
		if err := s.Validate(); err == nil {
			t.Errorf("into %q accepted", bad)
		}
	}
	s := sampleSession()
	s.Into = map[string]string{"Bad Member": "main"}
	if err := s.Validate(); err == nil {
		t.Error("a member key that is not a slug was accepted")
	}
	for _, good := range []string{"main", "feature/PLAT-412", "release/1.0"} {
		s := sampleSession()
		s.Into = map[string]string{"api": good}
		if err := s.Validate(); err != nil {
			t.Errorf("into %q rejected: %v", good, err)
		}
	}
}

func TestBaseBranch(t *testing.T) {
	for base, want := range map[string]string{
		"origin/main": "main", "origin/release/1.0": "release/1.0", "main": "main",
		"origin/HEAD": "", "HEAD": "", "": "", "upstream/HEAD": "",
	} {
		if got := BaseBranch(Member{Base: base}); got != want {
			t.Errorf("base %q: got %q, want %q", base, got, want)
		}
	}
}

func TestSessionMembersRoundTrip(t *testing.T) {
	s := sampleSession()
	s.SetMember("sdk", SessionMember{Branch: s.Branch, Created: true})
	s.SetMember("api", SessionMember{Branch: "feat/x", From: "feat/parent", BaseTip: "abc123"})
	s.SetMember("web", SessionMember{Branch: s.Branch})
	s.SetMember("lib", SessionMember{Ref: "feat/z", BaseTip: "def456"})
	body := string(EncodeSession(s))
	if strings.Index(body, "[members.api]") > strings.Index(body, "[members.sdk]") {
		t.Errorf("members should be written in a stable order:\n%s", body)
	}
	got, err := ParseSession([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	if got.Schema != SessionSchemaVersion || len(got.Members) != 3 {
		t.Errorf("schema = %d, members = %+v", got.Schema, got.Members)
	}
	if got.Members["api"] != (SessionMember{Branch: "feat/x", From: "feat/parent", BaseTip: "abc123"}) || got.Members["sdk"] != (SessionMember{Created: true}) {
		t.Errorf("members = %+v", got.Members)
	}
	if got.BranchFor("api") != "feat/x" || got.BranchFor("sdk") != s.Branch || got.BranchFor("unlisted") != s.Branch {
		t.Errorf("branches = %q %q %q", got.BranchFor("api"), got.BranchFor("sdk"), got.BranchFor("unlisted"))
	}
	for name, want := range map[string]string{"lib": "feat/z", "docs": "origin/HEAD"} {
		if ref := got.RefFor(Member{Name: name, Ref: "origin/HEAD"}); ref != want {
			t.Errorf("%s is at %q, want %q", name, ref, want)
		}
	}
	got.SetMember("sdk", SessionMember{})
	if _, ok := got.Members["sdk"]; ok {
		t.Error("an empty record should be dropped")
	}
}

func TestSessionValidateMembers(t *testing.T) {
	for name, bad := range map[string]map[string]SessionMember{
		"branch":      {"api": {Branch: "a b"}},
		"option":      {"api": {Branch: "-x"}},
		"from":        {"api": {From: "-x"}},
		"ref":         {"api": {Ref: "a b"}},
		"member name": {"Bad Member": {Created: true}},
	} {
		s := sampleSession()
		s.Members = bad
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
}

func TestLandingSessionKeepsItsSchema(t *testing.T) {
	old := "schema  = 1\npackage = \"p\"\nname    = \"s\"\ncreated = 2026-11-16T09:00:00Z\nbranch  = \"vmono/p/s\"\n\n[into]\napi = \"main\"\n"
	s, err := ParseSession([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if s.Schema != LandingSessionSchema || s.Into["api"] != "main" || s.BranchFor("api") != "vmono/p/s" {
		t.Errorf("session = %+v", s)
	}
	again, err := ParseSession(EncodeSession(s))
	if err != nil || again.Schema != LandingSessionSchema || again.Into["api"] != "main" {
		t.Errorf("saving must not convert it: %+v, %v", again, err)
	}
}
