package core

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

func TestRecordInitKeepsTheUsersText(t *testing.T) {
	c, env := newCore(t)
	m := request("demo", []string{env.NewSource(t, "alpha")}, []string{env.NewSource(t, "beta")})
	m.Context = "my context"
	p, s := newWork(t, c, m)
	before := testutil.Git(t, p.Dir, "rev-list", "--count", "HEAD")

	res, err := c.RecordInit(p, InitRecord{Purpose: "agent purpose", Overview: "first", Roles: map[string]string{"alpha": "agent role", "beta": "agent\nrole"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Set, []string{"overview", "role:beta"}) || !reflect.DeepEqual(res.Kept, []string{"purpose", "role:alpha"}) {
		t.Errorf("result = %+v", res)
	}
	p, _ = c.Load("demo")
	beta, _ := p.M.Member("beta")
	if strings.TrimSpace(p.M.Purpose) != "test purpose" || p.M.Context != "my context" || beta.Role != "agent role" || p.M.Overview() != "first" {
		t.Errorf("manifest = %+v", p.M)
	}
	if got := testutil.Git(t, p.Dir, "rev-list", "--count", "HEAD"); got == before {
		t.Error("the record should be committed to the package repo")
	}
	data, _ := os.ReadFile(filepath.Join(s.Dir, "AGENTS.md"))
	if !strings.Contains(string(data), "## Overview\n\nfirst") {
		t.Errorf("existing sessions should read the overview:\n%s", data)
	}

	// The agent may revise its own role and overview, until the user edits them.
	if _, err := c.RecordInit(p, InitRecord{Overview: "second", Roles: map[string]string{"beta": "revised"}}); err != nil {
		t.Fatal(err)
	}
	beta, _ = p.M.Member("beta")
	if beta.Role != "revised" || p.M.Overview() != "second" {
		t.Errorf("a revision should replace what init wrote: %q %q", beta.Role, p.M.Overview())
	}
	if err := c.SetRole(p, "beta", "mine"); err != nil {
		t.Fatal(err)
	}
	res, err = c.RecordInit(p, InitRecord{Overview: "third", Roles: map[string]string{"beta": "again"}})
	beta, _ = p.M.Member("beta")
	if err != nil || beta.Role != "mine" || len(res.Kept) != 1 {
		t.Errorf("an edited role is the user's: %q %+v %v", beta.Role, res, err)
	}
}

func TestRecordInitFillsAndRevisesAnEmptyPurpose(t *testing.T) {
	c, env := newCore(t)
	m := request("demo", []string{env.NewSource(t, "alpha")}, nil)
	m.Purpose = ""
	p := mustPackage(t, c, m)
	for _, text := range []string{"one", "two\nlines"} {
		if _, err := c.RecordInit(p, InitRecord{Overview: "o", Purpose: text}); err != nil {
			t.Fatal(err)
		}
		p, _ = c.Load("demo")
		if strings.TrimSpace(p.M.Purpose) != text {
			t.Errorf("purpose = %q, want %q", p.M.Purpose, text)
		}
	}
}

func TestRecordInitRefusesBadInput(t *testing.T) {
	c, env := newCore(t)
	p := mustPackage(t, c, request("demo", []string{env.NewSource(t, "alpha")}, nil))
	if _, err := c.RecordInit(p, InitRecord{Overview: " "}); err == nil {
		t.Error("an empty overview should be refused")
	}
	if _, err := c.RecordInit(p, InitRecord{Overview: "o", Roles: map[string]string{"ghost": "x"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown member: %v", err)
	}
	if p.M.Init != nil {
		t.Error("a refused record must not change the package")
	}
}

func TestDescribeOverviewAndShowDrift(t *testing.T) {
	c, env := newCore(t)
	p := mustPackage(t, c, request("demo", []string{env.NewSource(t, "alpha")}, nil))
	text, empty := "by hand", ""
	if err := c.Describe(p, DescribeOptions{Overview: &text}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMember(p, AddOptions{Source: env.NewSource(t, "beta")}); err != nil {
		t.Fatal(err)
	}
	if in := c.Show(p, nil).Init; in == nil || in.Overview != "by hand" || !reflect.DeepEqual(in.Added, []string{"beta"}) || len(in.Removed) != 0 {
		t.Errorf("show init = %+v", in)
	}
	if err := c.Describe(p, DescribeOptions{Overview: &empty}); err != nil || p.M.Init != nil {
		t.Errorf("clearing the overview: %v %+v", err, p.M.Init)
	}
}
