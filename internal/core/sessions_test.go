package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// twoSessions creates a package with member alpha and sessions "one" and
// "two" on default branches.
func twoSessions(t *testing.T, c *Core, env *testutil.Env, name string) (*Package, *Session, *Session, string) {
	t.Helper()
	a := env.NewSource(t, "alpha")
	p := mustPackage(t, c, request(name, []string{a}, nil))
	one, err := c.CreateSession(p, "one", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	two, err := c.CreateSession(p, "two", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return p, one, two, a
}

func TestAddMemberLandsInEverySession(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "grow")
	b := env.NewSource(t, "beta")

	if _, err := c.AddMember(p, AddOptions{Source: b, Role: "second"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		if br := gitx.CurrentBranch(filepath.Join(s.Dir, "beta")); br != s.S.Branch {
			t.Errorf("session %s: beta branch = %q, want %q", s.Name, br, s.S.Branch)
		}
		ign, _ := os.ReadFile(filepath.Join(s.Dir, ".gitignore"))
		if !strings.Contains(string(ign), "/beta/") {
			t.Errorf("session %s .gitignore not regenerated: %s", s.Name, ign)
		}
		agents, _ := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
		if !strings.Contains(string(agents), "second") {
			t.Errorf("session %s AGENTS.md does not list the new member", s.Name)
		}
	}
	if list, _ := gitx.WorktreeList(b); len(list) != 3 {
		t.Errorf("beta should be checked out once per session: %+v", list)
	}
}

func TestAddMemberRollsBackEverywhereWhenOneSessionFails(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "atomic")
	b := env.NewSource(t, "beta")

	// The second session's worktree path is occupied by a non-empty
	// directory, so its worktree add fails after the first session's work.
	testutil.WriteFile(t, filepath.Join(two.Dir, "beta", "squatter.txt"), "x")

	if _, err := c.AddMember(p, AddOptions{Source: b}); err == nil {
		t.Fatal("expected the second session to fail")
	}
	if _, err := os.Stat(filepath.Join(one.Dir, "beta")); !os.IsNotExist(err) {
		t.Error("the first session's worktree must be rolled back")
	}
	for _, s := range []*Session{one, two} {
		if gitx.BranchExists(b, s.S.Branch) {
			t.Errorf("branch %s must be removed on rollback", s.S.Branch)
		}
	}
	if list, _ := gitx.WorktreeList(b); len(list) != 1 {
		t.Errorf("stale worktree entries: %+v", list)
	}
	if reloaded, _ := c.Load("atomic"); len(reloaded.M.Members) != 1 {
		t.Errorf("manifest must be unchanged: %+v", reloaded.M.Members)
	}
	if _, err := os.Stat(filepath.Join(two.Dir, "beta", "squatter.txt")); err != nil {
		t.Error("rollback must not touch what was already there")
	}
}

func TestRemoveMemberRefusedWhenDirtyInOnlyOneSession(t *testing.T) {
	c, env := newCore(t)
	p, one, two, a := twoSessions(t, c, env, "shrink")
	b := env.NewSource(t, "beta")
	if _, err := c.AddMember(p, AddOptions{Source: b}); err != nil {
		t.Fatal(err)
	}

	testutil.WriteFile(t, filepath.Join(two.Dir, "beta", "wip.txt"), "x")
	_, err := c.RemoveMember(p, "beta", RemoveOptions{})
	var re *RefusedError
	if !errors.As(err, &re) || len(re.Unsafe) != 1 || re.Unsafe[0].Label() != "two/beta" {
		t.Fatalf("want refusal naming two/beta, got %v", err)
	}
	for _, s := range []*Session{one, two} {
		if _, err := os.Stat(filepath.Join(s.Dir, "beta")); err != nil {
			t.Errorf("refused removal must leave session %s intact", s.Name)
		}
	}
	if _, err := c.RemoveMember(p, "beta", RemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		if _, err := os.Stat(filepath.Join(s.Dir, "beta")); !os.IsNotExist(err) {
			t.Errorf("session %s should have lost beta", s.Name)
		}
	}
	if _, err := os.Stat(filepath.Join(one.Dir, "alpha")); err != nil {
		t.Error("other members stay")
	}
	_ = a
}

func TestApplyEditAppliesToEverySession(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "multi")
	b := env.NewSource(t, "beta")

	e := edited(p)
	e.Members = append(e.Members, manifest.Member{Source: b, Mode: manifest.ModeActive})
	if _, err := c.ApplyEdit(p, e, EditOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		if _, err := os.Stat(filepath.Join(s.Dir, "beta", "README.md")); err != nil {
			t.Errorf("session %s missing beta: %v", s.Name, err)
		}
	}

	// Dirty in one session: the plan lists it per session and the edit is refused.
	testutil.WriteFile(t, filepath.Join(one.Dir, "beta", "wip"), "x")
	e = edited(p)
	e.Members = e.Members[:1]
	ep, err := c.PlanEdit(p, e)
	if err != nil || len(ep.Unsafe) != 1 || ep.Unsafe[0].Session != "one" || len(ep.Sessions) != 2 {
		t.Fatalf("PlanEdit = %+v, %v", ep, err)
	}
	if _, err := c.ApplyEdit(p, e, EditOptions{}); !errors.Is(err, ErrRefused) {
		t.Fatalf("want refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(two.Dir, "beta")); err != nil {
		t.Error("a refused edit must change no session")
	}
	if _, err := c.ApplyEdit(p, e, EditOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		if _, err := os.Stat(filepath.Join(s.Dir, "beta")); !os.IsNotExist(err) {
			t.Errorf("session %s should have lost beta", s.Name)
		}
	}
}

func TestPackageTextReachesEverySession(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "text")
	c.Author = "agent:claude"

	if err := c.AddNote(p, "contracts v3 is frozen"); err != nil {
		t.Fatal(err)
	}
	purpose := "A brand new purpose."
	if err := c.Describe(p, DescribeOptions{Purpose: &purpose}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		agents, _ := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
		for _, want := range []string{"contracts v3 is frozen", "A brand new purpose.", "session " + s.Name, s.S.Branch} {
			if !strings.Contains(string(agents), want) {
				t.Errorf("session %s AGENTS.md missing %q", s.Name, want)
			}
		}
	}
	// The change is committed once, to the package repo, under its author.
	if log := strings.Join(subjects(t, p.Dir), "|"); !strings.HasPrefix(log, "vmono: describe purpose (agent:claude)|vmono: note (agent:claude)|") {
		t.Errorf("package log = %s", log)
	}
	// Session repos get no commit per note; the next sync commits renders.
	for _, s := range []*Session{one, two} {
		if got := subjects(t, s.Dir); len(got) != 1 {
			t.Errorf("session %s repo has extra commits: %v", s.Name, got)
		}
	}
	if _, err := c.SyncPackage(p, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{one, two} {
		if out := testutil.Git(t, s.Dir, "status", "--porcelain"); out != "" {
			t.Errorf("session %s should be clean after sync: %s", s.Name, out)
		}
	}
}

func TestSyncPackageSyncsEverySession(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "all")
	for _, s := range []*Session{one, two} {
		if err := os.RemoveAll(filepath.Join(s.Dir, "alpha")); err != nil {
			t.Fatal(err)
		}
	}
	res, err := c.SyncPackage(p, SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Session != "one" || res[1].Session != "two" {
		t.Fatalf("results = %+v", res)
	}
	for i, s := range []*Session{one, two} {
		if strings.Join(res[i].Recreated, ",") != "alpha" {
			t.Errorf("session %s recreated = %v", s.Name, res[i].Recreated)
		}
		if br := gitx.CurrentBranch(filepath.Join(s.Dir, "alpha")); br != s.S.Branch {
			t.Errorf("session %s alpha branch = %q", s.Name, br)
		}
	}
}

func TestPackageGitignoreKeepsOnlyTheManifest(t *testing.T) {
	c, env := newCore(t)
	p, _, _, _ := twoSessions(t, c, env, "ign")
	files := testutil.Git(t, p.Dir, "ls-files")
	if strings.TrimSpace(files) != ".gitignore\n.vmono/manifest.toml" {
		t.Errorf("package repo tracks %q", files)
	}
}

func TestDoctorChecksEverySessionAndReportsStrayDirs(t *testing.T) {
	c, env := newCore(t)
	p, one, two, _ := twoSessions(t, c, env, "chk")
	if res, _ := c.Doctor(false); res.Packages != 1 || res.Remaining() != 0 {
		t.Fatalf("healthy = %+v", res)
	}

	if err := os.RemoveAll(filepath.Join(two.Dir, "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := c.Doctor(false)
	var missing, stray bool
	for _, f := range res.Findings {
		if f.Kind == KindMissingWorktree && f.Session == "two" && f.Member == "alpha" {
			missing = true
		}
		if f.Kind == KindOrphanSessDir && strings.Contains(f.Message, "notes") {
			stray = true
		}
		if f.Session == "one" {
			t.Errorf("session one is healthy but got %+v", f)
		}
	}
	if !missing || !stray {
		t.Fatalf("findings = %+v", res.Findings)
	}

	res, err := c.Doctor(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(two.Dir, "alpha", "README.md")); err != nil {
		t.Error("session two's worktree should be recreated")
	}
	if res.Remaining() != 1 {
		t.Errorf("only the stray directory should remain: %+v", res.Findings)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "notes")); err != nil {
		t.Error("doctor must never delete a stray directory")
	}
	_ = one
}
