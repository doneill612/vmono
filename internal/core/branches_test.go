package core

import (
	"testing"
)

func TestListBranchesTellsLocalFromRemoteAndHowFarApart(t *testing.T) {
	c, env := newCore(t)
	a := env.NewSource(t, "alpha")
	splitBranch(t, a, "feat/split")
	commitIn(t, a, "unpushed.txt") // on no branch: the source is detached
	pushFromElsewhere(t, a, "theirs", "x.txt", "x\n")
	p := mustPackage(t, c, request("list", []string{a}, nil))

	list := c.ListBranches(p, "", true)
	if len(list) != 1 || list[0].ListErr != nil || list[0].FetchErr != nil {
		t.Fatalf("list = %+v", list)
	}
	got := map[string]BranchChoice{}
	for _, b := range list[0].Branches {
		got[b.Name] = b
	}
	if b := got["feat/split"]; b.Remote || b.Ref != "refs/heads/feat/split" || b.Counterpart != "origin/feat/split" || b.Ahead != 1 || b.Behind != 1 {
		t.Errorf("local = %+v", b)
	}
	if b := got["origin/feat/split"]; !b.Remote || b.Branch != "feat/split" || b.Ref != "refs/remotes/origin/feat/split" || b.Counterpart != "" {
		t.Errorf("remote = %+v", b)
	}
	if b := got["main"]; b.Ahead != 0 || b.Behind != 0 || b.Counterpart != "origin/main" {
		t.Errorf("a branch level with its remote counts nothing: %+v", b)
	}
	if _, ok := got["theirs"]; ok || got["origin/theirs"].Ref == "" {
		t.Error("a branch only the remote has is listed as the remote's alone")
	}
}
