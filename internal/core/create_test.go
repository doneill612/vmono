package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

// featureBranch creates branch in src from main with one extra commit, and
// returns the new tip. The source's checkout stays on main.
func featureBranch(t *testing.T, src, branch string) string {
	t.Helper()
	testutil.Git(t, src, "checkout", "--quiet", "-b", branch)
	testutil.WriteFile(t, filepath.Join(src, branch+".txt"), branch+"\n")
	testutil.Git(t, src, "add", "-A")
	testutil.Git(t, src, "commit", "--quiet", "-m", "work on "+branch)
	tip, err := gitx.ResolveRef(src, branch)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, src, "checkout", "--quiet", "main")
	return tip
}

// commitIn writes a file in dir and commits it.
func commitIn(t *testing.T, dir, name string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(dir, name), name+"\n")
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "add "+name)
}

// refused checks that a create failed and left nothing behind.
func refused(t *testing.T, p *Package, name, what string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: got %v, want %q", what, err, want)
	}
	if _, err := os.Stat(p.SessionDir(name)); !os.IsNotExist(err) {
		t.Errorf("%s: a refused session must leave no directory", what)
	}
}

func TestCreateSessionNamesBranches(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	p := mustPackage(t, c, request("names", []string{a, b}, nil))
	for name, tc := range map[string]struct {
		opts        SessionOptions
		alpha, beta string
	}{
		"plain":    {SessionOptions{}, "plain", "plain"},
		"prefixed": {SessionOptions{Prefix: "feat/"}, "feat/prefixed", "feat/prefixed"},
		"named":    {SessionOptions{Branch: "fix/x"}, "fix/x", "fix/x"},
		"mixed":    {SessionOptions{Prefix: "feat", BranchByMember: map[string]string{"beta": "other"}}, "feat/mixed", "other"},
	} {
		s, err := c.CreateSession(p, name, tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		loaded, err := c.LoadSession(p, name)
		if err != nil {
			t.Fatal(err)
		}
		for member, want := range map[string]string{"alpha": tc.alpha, "beta": tc.beta} {
			if got := gitx.CurrentBranch(filepath.Join(s.Dir, member)); got != want || loaded.BranchFor(member) != want || !loaded.S.Members[member].Created {
				t.Errorf("%s: %s is on %q, recorded %q %+v; want %q", name, member, got, loaded.BranchFor(member), loaded.S.Members[member], want)
			}
		}
		if _, ok := gitx.UpstreamOf(a, tc.alpha); ok {
			t.Errorf("%s: a new branch tracks nothing", name)
		}
	}
}

// TestNewBranchesStartFromTheBase covers a source whose checkout is elsewhere:
// neither the branch it is on nor its local main is where work starts.
func TestNewBranchesStartFromTheBase(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	commitIn(t, a, "local-only.txt")
	featureBranch(t, b, "develop")
	testutil.Git(t, b, "checkout", "--quiet", "develop")
	p := mustPackage(t, c, request("start", []string{a, b}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"alpha": a, "beta": b} {
		if got, want := sha(t, filepath.Join(s.Dir, name), "HEAD"), sha(t, src, "origin/main"); got != want {
			t.Errorf("%s starts at %s, want origin/main %s", name, got, want)
		}
	}
}

func TestCreateSessionRefusesBranchesItCannotWorkOn(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	testutil.Git(t, a, "branch", "taken")
	testutil.Git(t, a, "branch", "feat")
	pushFromElsewhere(t, a, "theirs", "x.txt", "x\n") // only a fetch reveals it
	p := mustPackage(t, c, request("bad", []string{a}, nil))

	for what, tc := range map[string]struct {
		opts SessionOptions
		want string
	}{
		"not a name":        {SessionOptions{Branch: "a..b"}, "not a valid branch name"},
		"git rejects it":    {SessionOptions{Branch: "a@{b"}, "not a valid branch name"},
		"option":            {SessionOptions{Branch: "-x"}, "not a valid branch name"},
		"base":              {SessionOptions{Branch: "main"}, "its work merges into"},
		"git cannot hold":   {SessionOptions{Branch: "feat/x"}, "conflicts with existing branch"},
		"exists locally":    {SessionOptions{Branch: "taken"}, "already exists"},
		"exists on remote":  {SessionOptions{Branch: "theirs"}, "already on origin"},
		"unknown member":    {SessionOptions{BranchByMember: map[string]string{"ghost": "x"}}, "not a member"},
		"branch and track":  {SessionOptions{Branch: "x", Track: "theirs"}, "--branch and --track"},
		"prefix and branch": {SessionOptions{Prefix: "p", Branch: "x"}, "--prefix"},
	} {
		_, err := c.CreateSession(p, "work", tc.opts)
		refused(t, p, "work", what, err, tc.want)
	}
	var re *RemoteBranchExistsError
	if _, err := c.CreateSession(p, "work", SessionOptions{Branch: "theirs"}); !errors.As(err, &re) || re.Member != "alpha" || re.Remote != "origin" {
		t.Errorf("err = %v", err)
	}
	// A session named after the base branch is the likeliest way to hit it.
	_, err := c.CreateSession(p, "main", SessionOptions{})
	refused(t, p, "main", "named main", err, "--prefix")
}

func TestReusedBranchIsNotRecordedAsCreated(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	tip := featureBranch(t, a, "mine")
	p := mustPackage(t, c, request("reuse", []string{a}, nil))
	s, err := c.CreateSession(p, "work", SessionOptions{Branch: "mine", ReuseBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	if sha(t, filepath.Join(s.Dir, "alpha"), "HEAD") != tip || s.S.Members["alpha"].Created {
		t.Errorf("members = %+v", s.S.Members)
	}
}

func TestFromBuildsOnAnotherBranch(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	tip := featureBranch(t, a, "parent")
	p := mustPackage(t, c, request("from", []string{a, b}, nil))

	s, err := c.CreateSession(p, "child", SessionOptions{From: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if sha(t, filepath.Join(s.Dir, "alpha"), "HEAD") != tip || sha(t, filepath.Join(s.Dir, "beta"), "HEAD") != sha(t, b, "origin/main") {
		t.Error("alpha starts from parent; beta, which lacks it, from its base")
	}
	loaded, err := c.LoadSession(p, "child")
	if err != nil {
		t.Fatal(err)
	}
	if m := loaded.S.Members["alpha"]; m.From != "parent" || m.BaseTip != tip || !m.Created {
		t.Errorf("alpha = %+v", m)
	}
	if m := loaded.S.Members["beta"]; m.From != "" {
		t.Errorf("beta = %+v", m)
	}
	commitIn(t, filepath.Join(s.Dir, "alpha"), "child.txt")
	if _, m := status(t, c, s, "alpha"); m.OwnCommits != 1 || m.BuiltOn != "parent" {
		t.Errorf("only the child's commit is its own: %+v", m)
	}

	for what, tc := range map[string]struct {
		opts SessionOptions
		want string
	}{
		"nobody has it":   {SessionOptions{From: "nope"}, "no member"},
		"member lacks it": {SessionOptions{FromByMember: map[string]string{"beta": "parent"}}, "beta"},
		"itself":          {SessionOptions{From: "again"}, "built on itself"},
		"with track":      {SessionOptions{Track: "x", From: "parent"}, "--track and --from"},
	} {
		_, err := c.CreateSession(p, "again", tc.opts)
		refused(t, p, "again", what, err, tc.want)
	}
	if _, err := c.CreateSession(p, "again", SessionOptions{From: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestMembersAddedLaterAreRecorded(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	p := mustPackage(t, c, request("later", []string{a}, nil))
	if _, err := c.CreateSession(p, "work", SessionOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMember(p, AddOptions{Source: b}); err != nil {
		t.Fatal(err)
	}
	s, err := c.LoadSession(p, "work")
	if err != nil || !s.S.Members["beta"].Created || s.BranchFor("beta") != "work" {
		t.Fatalf("members = %+v, %v", s.S.Members, err)
	}
	if _, err := c.RemoveMember(p, "beta", RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if s, err = c.LoadSession(p, "work"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.S.Members["beta"]; ok {
		t.Errorf("a member that left should be forgotten: %+v", s.S.Members)
	}
	next := clone(p.M)
	next.Members = append(next.Members, manifest.Member{Source: b, Mode: manifest.ModeActive})
	if _, err := c.ApplyEdit(p, next, EditOptions{ReuseBranch: true}); err != nil {
		t.Fatal(err)
	}
}

// TestReferenceOnALocalBranchIsSafeToDelete covers a source with no
// origin/HEAD, whose reference members sit on the branch it has checked out.
// Its commits stay in the source repo, so removing the worktree loses nothing,
// but commits made on the detached HEAD itself would be lost.
func TestReferenceOnALocalBranchIsSafeToDelete(t *testing.T) {
	c, env := newCore(t)
	a, ref := env.NewSource(t, "alpha"), env.NewSource(t, "contracts")
	testutil.Git(t, ref, "remote", "set-head", "origin", "--delete")
	testutil.Git(t, ref, "checkout", "--quiet", "-b", "feature")
	commitIn(t, ref, "local.txt")
	_, s := newWork(t, c, request("refs", []string{a}, []string{ref}))
	wt := filepath.Join(s.Dir, "_ref", "contracts")
	if sha(t, wt, "HEAD") != sha(t, ref, "feature") {
		// git 2.48 and newer set origin/HEAD again on fetch.
		testutil.Git(t, wt, "checkout", "--quiet", "--detach", "feature")
	}
	if bad := unsafeOf(c.SafetyCheck(s)); len(bad) != 0 {
		t.Errorf("unsafe = %+v", bad)
	}

	commitIn(t, wt, "stray.txt")
	bad := unsafeOf(c.SafetyCheck(s))
	if len(bad) != 1 || bad[0].Name != "contracts" || bad[0].Unpushed != 1 {
		t.Errorf("a commit only the worktree holds must be unsafe: %+v", bad)
	}
	testutil.Git(t, wt, "reset", "--quiet", "--hard", "HEAD~1")
	if _, err := c.DeleteSession(s, DeleteOptions{}); err != nil {
		t.Errorf("remove: %v", err)
	}
}

func TestClonePackageCopiesEverythingButModes(t *testing.T) {
	c, env := newCore(t)
	a, b := env.NewSource(t, "alpha"), env.NewSource(t, "beta")
	req := request("docs", []string{a}, []string{b})
	req.Harness, req.Context = "claude", "ticket 7"
	req.Members[1].Ref = "main"
	req.Members[1].Setup = manifest.Commands{"make deps"}
	req.Notes = []manifest.Note{{At: time.Date(2026, 11, 14, 10, 5, 0, 0, time.UTC), By: "user", Text: "frozen"}}
	req.Init = &manifest.Init{
		At: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), By: "agent:claude",
		Members: []string{"alpha", "beta"}, Overview: "how they fit",
		Suggestions: []manifest.Suggestion{{Member: "beta", Kind: "teardown", Command: "make down"}},
	}
	src := mustPackage(t, c, req)
	before := string(manifest.Encode(src.M))

	m, notes, err := CloneManifest(src, "work", map[string]string{"beta": manifest.ModeActive})
	if err != nil || len(notes) != 0 {
		t.Fatal(notes, err)
	}
	p, err := c.ClonePackage(src, m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := manifest.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	beta, _ := got.Member("beta")
	if beta.Mode != manifest.ModeActive || beta.Path != "beta" || beta.Base != "main" || beta.Ref != "" {
		t.Errorf("beta did not become active: %+v", beta)
	}
	if len(beta.Setup) != 1 {
		t.Errorf("setup was lost: %+v", beta)
	}
	if alpha, _ := got.Member("alpha"); alpha.Mode != manifest.ModeActive || alpha.Role != "role of alpha" {
		t.Errorf("alpha changed: %+v", alpha)
	}
	if got.Name != "work" || got.Harness != "claude" || strings.TrimSpace(got.Context) != "ticket 7" || strings.TrimSpace(got.Purpose) != "test purpose" {
		t.Errorf("package text was not copied: %+v", got)
	}
	if len(got.Notes) != 1 || got.Overview() != "how they fit" || len(got.Pending()) != 1 {
		t.Errorf("notes or init were not copied: %+v", got)
	}
	if drift := got.DriftText(); drift != "" {
		t.Errorf("a clone must not report drift, got %q", drift)
	}
	if log := testutil.Git(t, p.Dir, "log", "--format=%s"); !strings.Contains(log, "vmono: clone docs as work") {
		t.Errorf("commit message: %q", log)
	}
	if after := string(manifest.Encode(src.M)); after != before {
		t.Errorf("cloning changed the source:\n%s", after)
	}
}

func TestClonePackageRefusals(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	req := request("twice", nil, []string{a, a})
	req.Members[1].Name = "alpha-old"
	src := mustPackage(t, c, req)

	if _, _, err := CloneManifest(src, "other", map[string]string{"nope": manifest.ModeActive}); err == nil || !strings.Contains(err.Error(), `no member "nope"`) {
		t.Errorf("unknown member: %v", err)
	}
	if _, _, err := CloneManifest(src, "other", map[string]string{"alpha": "writable"}); err == nil || !strings.Contains(err.Error(), "mode must be") {
		t.Errorf("unknown mode: %v", err)
	}
	m, _, err := CloneManifest(src, "other", map[string]string{"alpha": manifest.ModeActive, "alpha-old": manifest.ModeActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ClonePackage(src, m); err == nil || !strings.Contains(err.Error(), "same repo") {
		t.Errorf("two active members of one repo: %v", err)
	}
	if m, _, err = CloneManifest(src, "twice", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ClonePackage(src, m); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("taken name: %v", err)
	}
}

func TestSetModeDropsARefNothingCanMergeInto(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	featureBranch(t, a, "develop")
	testutil.Git(t, a, "tag", "v3")
	for from, want := range map[string]string{
		"origin/HEAD": "origin/HEAD", "HEAD": "HEAD", "develop": "develop", "origin/main": "origin/main",
		"v3": "", "main~0": "", "origin/main~0": "", sha(t, a, "main"): "",
	} {
		mem := manifest.Member{Name: "alpha", Source: a, Mode: manifest.ModeReference, Ref: from}
		note := SetMode(&mem, manifest.ModeActive)
		if mem.Base != want || (note == "") != (want != "") {
			t.Errorf("ref %q: base %q, note %q", from, mem.Base, note)
		}
	}

	req := request("pinned", []string{a}, nil)
	req.Members[0].Base = "develop"
	src := mustPackage(t, c, req)
	m, notes, err := CloneManifest(src, "reading", map[string]string{"alpha": manifest.ModeReference})
	if err != nil || len(notes) != 0 || m.Members[0].Ref != "develop" {
		t.Errorf("base did not become the ref: %+v %v %v", m, notes, err)
	}
}
