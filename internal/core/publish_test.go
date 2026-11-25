package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doneill612/vmono/internal/gitx"
	"github.com/doneill612/vmono/internal/manifest"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestPublicationOf(t *testing.T) {
	_, env := newCore(t)
	a := env.NewSource(t, "alpha")
	featureBranch(t, a, "feat")
	of := func() publication { return publicationOf(gitx.LiveRefs(a), a, "feat", []string{"main"}) }

	if p := of(); p.published() || p.deleted() {
		t.Errorf("never pushed: %+v", p)
	}
	testutil.Git(t, a, "branch", "--set-upstream-to=origin/main", "feat")
	if p := of(); p != (publication{}) {
		t.Errorf("an upstream of main is what it was cut from: %+v", p)
	}
	testutil.Git(t, a, "push", "--quiet", "origin", "feat")
	if p := of(); !p.published() || p.upstream || p.ref != "refs/remotes/origin/feat" {
		t.Errorf("pushed without an upstream: %+v", p)
	}
	testutil.Git(t, a, "branch", "--set-upstream-to=origin/feat", "feat")
	if p := of(); !p.published() || !p.upstream || p.remote != "origin" || p.dest != "feat" {
		t.Errorf("pushed with an upstream: %+v", p)
	}
	testutil.Git(t, remoteOf(env, "alpha"), "branch", "-D", "feat")
	testutil.Git(t, a, "fetch", "--quiet", "--prune")
	if p := of(); p.published() || !p.deleted() {
		t.Errorf("deleted on the remote: %+v", p)
	}
	if p := publicationOf(gitx.LiveRefs(a), a, "main", []string{"main"}); p != (publication{}) {
		t.Errorf("the base branch itself is never a place to publish to: %+v", p)
	}
}

func TestBaseOf(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	_, s := newWork(t, c, request("base", []string{a}, nil))
	mem := s.Pkg.M.Members[0]
	base := func() memberBase {
		t.Helper()
		b, err := baseOf(gitx.LiveRefs(a), s, mem, a, nil)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if b := base(); b.ref != "origin/HEAD" || b.from || len(b.refs()) != 1 {
		t.Errorf("own base: %+v", b)
	}
	s.S.SetMember("alpha", manifest.SessionMember{From: "parent"})
	if b := base(); b.lost != "parent" || b.ref != "origin/HEAD" {
		t.Errorf("a parent that is not there: %+v", b)
	}
	featureBranch(t, a, "parent")
	if b := base(); b.ref != "refs/heads/parent" || !b.from || len(b.refs()) != 2 {
		t.Errorf("local parent: %+v", b)
	}
	testutil.Git(t, a, "push", "--quiet", "-u", "origin", "parent")
	pushFromElsewhere(t, a, "parent", "more.txt", "more\n")
	testutil.Git(t, a, "fetch", "--quiet")
	if b := base(); b.ref != "refs/remotes/origin/parent" {
		t.Errorf("the remote copy is ahead: %+v", b)
	}
	if got := baseBranches(s, mem, a); len(got) != 2 || got[0] != "main" || got[1] != "parent" {
		t.Errorf("base branches = %v", got)
	}
	s.S.SetMember("alpha", manifest.SessionMember{From: "origin/parent"})
	if got := baseBranches(s, mem, a); got[1] != "parent" {
		t.Errorf("base branches = %v", got)
	}
}

func TestMergedInto(t *testing.T) {
	work := func(t *testing.T) (src string) {
		_, env := newCore(t)
		src = env.NewSource(t, "alpha")
		testutil.Git(t, src, "checkout", "--quiet", "-b", "feat")
		commitIn(t, src, "a.txt")
		commitIn(t, src, "b.txt")
		testutil.Git(t, src, "checkout", "--quiet", "main")
		commitIn(t, src, "other.txt")
		return src
	}
	for want, merge := range map[string]func(t *testing.T, src string){
		"":           func(*testing.T, string) {},
		MergedCommit: func(t *testing.T, src string) { testutil.Git(t, src, "merge", "--quiet", "--no-edit", "feat") },
		MergedRebase: func(t *testing.T, src string) { testutil.Git(t, src, "cherry-pick", "feat~1", "feat") },
		MergedSquash: func(t *testing.T, src string) {
			testutil.Git(t, src, "merge", "--quiet", "--squash", "feat")
			testutil.Git(t, src, "commit", "--quiet", "-m", "squashed")
		},
	} {
		t.Run("by "+want, func(t *testing.T) {
			src := work(t)
			merge(t, src)
			for range 2 { // the second answer is a remembered one
				if got := mergedInto(src, "feat", "main"); got != want {
					t.Errorf("mergedInto = %q, want %q", got, want)
				}
			}
			if how, base := mergedIntoAny(src, "feat", []string{"feat~2", "main"}); how != want || (base == "main") != (want != "") {
				t.Errorf("mergedIntoAny = %q, %q", how, base)
			}
		})
	}
}

func TestMergeCacheSurvivesABadFile(t *testing.T) {
	_, env := newCore(t)
	file := filepath.Join(env.Root, "state", "merged.json")
	for _, body := range []string{"not json", "null", "[]"} {
		testutil.WriteFile(t, file, body)
		m := &mergeCache{}
		if got := m.lookup("k", func() string { return MergedSquash }); got != MergedSquash {
			t.Errorf("%q: lookup = %q", body, got)
		}
		m = &mergeCache{}
		if got := m.lookup("k", func() string { return "worked out again" }); got != MergedSquash {
			t.Errorf("%q: not remembered, got %q", body, got)
		}
	}
	m := &mergeCache{}
	for i := range maxMerges + 20 {
		m.lookup(string(rune('a'))+string(rune(i)), func() string { return "" })
	}
	if len(m.how) > maxMerges {
		t.Errorf("%d answers kept", len(m.how))
	}
	if left, _ := filepath.Glob(filepath.Join(env.Root, "state", "*.tmp")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
	if _, err := os.Stat(file); err != nil {
		t.Error(err)
	}
}
