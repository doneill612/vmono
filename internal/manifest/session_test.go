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
	s.ApplyDefaults("vmono/")
	return s
}

func TestSessionDefaultBranchIncludesPackage(t *testing.T) {
	s := sampleSession()
	if s.Branch != "vmono/platform-auth/token-refresh" {
		t.Errorf("branch = %q", s.Branch)
	}
	s = &Session{Package: "p", Name: "s", Branch: "feat/PLAT-412"}
	s.ApplyDefaults("vmono/")
	if s.Branch != "feat/PLAT-412" {
		t.Errorf("explicit branch overwritten: %q", s.Branch)
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
		"bad name":      func(s *Session) { s.Name = "Bad_Name" },
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
	s.Into = "feature/PLAT-412"
	s.Finished = time.Date(2026, 11, 17, 15, 4, 5, 0, time.UTC)
	got, err := ParseSession(EncodeSession(s))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, EncodeSession(s))
	}
	if got.Into != s.Into || !got.Finished.Equal(s.Finished) {
		t.Errorf("round trip mismatch: into=%q finished=%v", got.Into, got.Finished)
	}

	plain, err := ParseSession(EncodeSession(sampleSession()))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Into != "" || !plain.Finished.IsZero() {
		t.Errorf("unset fields must stay unset: %+v", plain)
	}
	body := string(EncodeSession(sampleSession()))
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
	if s.Into != "" || !s.Finished.IsZero() {
		t.Errorf("old file gained values: %+v", s)
	}
}

func TestSessionValidateInto(t *testing.T) {
	for _, bad := range []string{"-x", "a b", "a..b", "/x", "x/", "x.lock", "a~1", "a:b", "vmono/platform-auth/token-refresh"} {
		s := sampleSession()
		s.Into = bad
		if err := s.Validate(); err == nil {
			t.Errorf("into %q accepted", bad)
		}
	}
	for _, good := range []string{"main", "feature/PLAT-412", "release/1.0"} {
		s := sampleSession()
		s.Into = good
		if err := s.Validate(); err != nil {
			t.Errorf("into %q rejected: %v", good, err)
		}
	}
}

func TestIntoFor(t *testing.T) {
	cases := []struct {
		into, base string
		want       string
		explicit   bool
	}{
		{"", "origin/main", "main", false},
		{"", "origin/release/1.0", "release/1.0", false},
		{"", "main", "main", false},
		{"", "origin/HEAD", "", false},
		{"", "HEAD", "", false},
		{"", "", "", false},
		{"feature/x", "origin/main", "feature/x", true},
	}
	for _, c := range cases {
		s := sampleSession()
		s.Into = c.into
		got, explicit := s.IntoFor(Member{Base: c.base})
		if got != c.want || explicit != c.explicit {
			t.Errorf("into=%q base=%q: got (%q, %v), want (%q, %v)", c.into, c.base, got, explicit, c.want, c.explicit)
		}
	}
}
