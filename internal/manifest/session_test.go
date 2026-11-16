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
