package manifest

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInitRoundTrip(t *testing.T) {
	m := sample()
	if strings.Contains(string(Encode(m)), "[init]") {
		t.Fatal("a package never initialized must not carry an init table")
	}
	m.Init = &Init{
		At: time.Date(2026, 11, 24, 22, 0, 0, 0, time.UTC), By: "agent:claude",
		Members:  []string{"api-gateway", "contracts"},
		Overview: "Two repos.\n\n- \"quoted\" `code` \\ and \"\"\" fences\n",
		Purpose:  "One line", Roles: map[string]string{"contracts": "Specs.", "api-gateway": "A \"b\""},
	}
	got, err := Parse(Encode(m))
	if err != nil {
		t.Fatalf("%v\n%s", err, Encode(m))
	}
	if !got.Init.At.Equal(m.Init.At) {
		t.Errorf("at = %v", got.Init.At)
	}
	got.Init.At = m.Init.At
	if !reflect.DeepEqual(got.Init, m.Init) {
		t.Errorf("init = %+v\nwant %+v", got.Init, m.Init)
	}
	if len(got.Members) != 2 || len(got.Notes) != 1 || got.Context != m.Context {
		t.Errorf("the init table swallowed other keys: %+v", got)
	}
}

func TestInitNeedsAnOverview(t *testing.T) {
	m := sample()
	m.Init = &Init{Overview: " \n"}
	if m.Validate() == nil {
		t.Error("an empty overview should not validate")
	}
}

func TestInitDrift(t *testing.T) {
	m := sample()
	if a, r := m.InitDrift(); a != nil || r != nil {
		t.Errorf("never initialized: %v %v", a, r)
	}
	m.Init = &Init{Overview: "x", Members: []string{"contracts", "gone"}}
	a, r := m.InitDrift()
	if !reflect.DeepEqual(a, []string{"api-gateway"}) || !reflect.DeepEqual(r, []string{"gone"}) {
		t.Errorf("drift = %v, %v", a, r)
	}
	next := sample()
	next.Init = &Init{Overview: "y"}
	if !Diff(m, next).MetaChanged {
		t.Error("a changed overview needs files regenerated")
	}
}
