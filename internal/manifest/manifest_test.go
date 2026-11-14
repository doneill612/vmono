package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() *Manifest {
	m := &Manifest{
		Schema:  1,
		Name:    "platform-auth",
		Created: time.Date(2026, 11, 13, 21, 20, 0, 0, time.FixedZone("EST", -5*3600)),
		Harness: "claude",
		Purpose: "Move token refresh.\nGateway owns it.\n",
		Context: "Ticket PLAT-412. \"quotes\" and \\ backslash.\n",
		Members: []Member{
			{Name: "api-gateway", Source: "/src/api-gateway", Remote: "git@x:o/api.git", Mode: ModeActive, Role: "Owns refresh.", Copy: []string{".env.local"}},
			{Name: "contracts", Source: "/src/contracts", Mode: ModeReference, Role: "Specs."},
		},
		Notes: []Note{{At: time.Date(2026, 11, 14, 10, 5, 0, 0, time.UTC), By: "agent:claude", Text: "v3 frozen\twith tab"}},
	}
	m.ApplyDefaults("vmono/")
	return m
}

func TestDefaults(t *testing.T) {
	m := sample()
	if m.Branch != "vmono/platform-auth" {
		t.Errorf("branch = %q", m.Branch)
	}
	if m.Members[0].Path != "api-gateway" || m.Members[0].Base != "origin/HEAD" {
		t.Errorf("active defaults wrong: %+v", m.Members[0])
	}
	if m.Members[1].Path != "_ref/contracts" || m.Members[1].Ref != "origin/HEAD" {
		t.Errorf("reference defaults wrong: %+v", m.Members[1])
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	m := sample()
	got, err := Parse(Encode(m))
	if err != nil {
		t.Fatalf("parse of encoded manifest: %v\n%s", err, Encode(m))
	}
	if got.Purpose != m.Purpose || got.Context != m.Context {
		t.Errorf("text mismatch: %q / %q", got.Purpose, got.Context)
	}
	if len(got.Members) != 2 || got.Members[0].Copy[0] != ".env.local" || got.Members[1].Mode != ModeReference {
		t.Errorf("members mismatch: %+v", got.Members)
	}
	if len(got.Notes) != 1 || got.Notes[0].Text != m.Notes[0].Text || !got.Notes[0].At.Equal(m.Notes[0].At) {
		t.Errorf("notes mismatch: %+v", got.Notes)
	}
	if !got.Created.Equal(m.Created) {
		t.Errorf("created mismatch: %v", got.Created)
	}
}

func TestEncodeControlCharsStayValid(t *testing.T) {
	m := sample()
	m.Members[0].Role = "bell\a vt\v del\x7f"
	if _, err := Parse(Encode(m)); err != nil {
		t.Fatalf("control chars produced invalid toml: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*Manifest){
		"bad name":         func(m *Manifest) { m.Name = "Bad_Name" },
		"dup member":       func(m *Manifest) { m.Members[1].Name = "api-gateway" },
		"bad mode":         func(m *Manifest) { m.Members[0].Mode = "weird" },
		"abs path":         func(m *Manifest) { m.Members[0].Path = "/etc" },
		"traversal":        func(m *Manifest) { m.Members[0].Path = "../x" },
		"reserved":         func(m *Manifest) { m.Members[0].Path = ".git" },
		"active under ref": func(m *Manifest) { m.Members[0].Path = "_ref/x" },
		"ref not under":    func(m *Manifest) { m.Members[1].Path = "contracts" },
		"dup path": func(m *Manifest) {
			m.Members[1].Path = "_ref/x"
			m.Members[0].Mode = ModeReference
			m.Members[0].Path = "_ref/x"
		},
		"empty source": func(m *Manifest) { m.Members[0].Source = "" },
	}
	for name, mutate := range cases {
		m := sample()
		mutate(m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if err := sample().Validate(); err != nil {
		t.Errorf("valid sample rejected: %v", err)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	body := string(Encode(sample())) + "\nbogus = 1\n"
	if _, err := Parse([]byte(body)); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("expected unknown key error, got %v", err)
	}
}

func TestSaveLoadAndFind(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, sample()); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "api-gateway", "src")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(deep)
	if err != nil || got != dir {
		t.Errorf("Find = %q, %v; want %q", got, err, dir)
	}
	t.Setenv("VMONO_ROOT", dir)
	if got, err := Find(t.TempDir()); err != nil || got != dir {
		t.Errorf("Find via VMONO_ROOT = %q, %v", got, err)
	}
	t.Setenv("VMONO_ROOT", "")
	if _, err := Find(t.TempDir()); err == nil {
		t.Error("expected not-found outside a package")
	}
}

func TestDiff(t *testing.T) {
	oldM := sample()
	newM := sample()
	if !Diff(oldM, newM).Empty() {
		t.Fatal("identical manifests must diff empty")
	}

	newM.Members[0].Role = "changed"
	newM.Members = append(newM.Members, Member{Name: "sdk", Source: "/src/sdk", Mode: ModeActive, Path: "sdk", Base: "origin/HEAD"})
	newM.Members[1].Mode = ModeActive
	newM.Members[1].Path = "contracts"
	newM.Purpose = "new purpose"
	p := Diff(oldM, newM)
	if len(p.Update) != 1 || p.Update[0].Name != "api-gateway" {
		t.Errorf("update wrong: %+v", p.Update)
	}
	if len(p.Add) != 1 || p.Add[0].Name != "sdk" {
		t.Errorf("add wrong: %+v", p.Add)
	}
	if len(p.Switch) != 1 || p.Switch[0].Old.Mode != ModeReference {
		t.Errorf("switch wrong: %+v", p.Switch)
	}
	if !p.MetaChanged {
		t.Error("purpose change should set MetaChanged")
	}

	gone := sample()
	gone.Members = gone.Members[:1]
	p = Diff(oldM, gone)
	if len(p.Remove) != 1 || p.Remove[0].Name != "contracts" || len(p.Removals()) != 1 {
		t.Errorf("remove wrong: %+v", p)
	}

	moved := sample()
	moved.Members[0].Source = "/elsewhere"
	p = Diff(oldM, moved)
	if len(p.Remove) != 1 || len(p.Add) != 1 {
		t.Errorf("source change should remove+add: %+v", p)
	}
}

func TestSlugifyAndRecentNotes(t *testing.T) {
	if got := Slugify("My_Repo.v2"); got != "my-repo-v2" {
		t.Errorf("Slugify = %q", got)
	}
	m := sample()
	for i := 0; i < 30; i++ {
		m.Notes = append(m.Notes, Note{At: time.Date(2026, 11, 15, 0, i, 0, 0, time.UTC), By: "user", Text: "n"})
	}
	notes := m.RecentNotes(MaxNotesRendered)
	if len(notes) != MaxNotesRendered || notes[0].At.Before(notes[1].At) {
		t.Errorf("RecentNotes not newest-first/capped: %d", len(notes))
	}
}
