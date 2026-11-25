package manifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestSuggestionsRoundTripAndPending(t *testing.T) {
	m := sample()
	a := Suggestion{Member: "contracts", Kind: SuggestSetup, Command: `echo "hi" \n`, Reason: "Makefile"}
	b := Suggestion{Member: "contracts", Kind: SuggestTeardown, Command: "make down"}
	gone := Suggestion{Member: "gone", Kind: SuggestSetup, Command: "x"}
	dismissed := Suggestion{Member: "contracts", Kind: SuggestSetup, Command: "no"}
	m.Init = &Init{Overview: "o", Members: []string{"contracts"}, Roles: map[string]string{"contracts": "r"},
		Suggestions: []Suggestion{a, b, a, gone, dismissed}, Settled: []Suggestion{dismissed}}
	got, err := Parse(Encode(m))
	if err != nil {
		t.Fatalf("%v\n%s", err, Encode(m))
	}
	if !reflect.DeepEqual(got.Init.Suggestions, m.Init.Suggestions) || !reflect.DeepEqual(got.Init.Settled, m.Init.Settled) || len(got.Members) != 2 {
		t.Errorf("init = %+v", got.Init)
	}
	if p := got.Pending(); !reflect.DeepEqual(p, []Suggestion{a, b}) {
		t.Errorf("pending = %+v", p)
	}
	mem, _ := got.Member("contracts")
	mem.Teardown = Commands{"make down"}
	if p := got.Pending(); !reflect.DeepEqual(p, []Suggestion{a}) {
		t.Errorf("a command the member has is settled: %+v", p)
	}
}

func TestSuggestionValidate(t *testing.T) {
	ok := Suggestion{Member: "a", Kind: SuggestSetup, Command: "pnpm install", Reason: "pnpm-lock.yaml"}
	if err := ok.Validate(); err != nil {
		t.Error(err)
	}
	for name, change := range map[string]func(*Suggestion){
		"kind":      func(s *Suggestion) { s.Kind = "copy" },
		"empty":     func(s *Suggestion) { s.Command = " " },
		"newline":   func(s *Suggestion) { s.Command = "a\nb" },
		"escape":    func(s *Suggestion) { s.Command = "a\x1b[2K" },
		"bidi":      func(s *Suggestion) { s.Command = "a‮b" },
		"reason":    func(s *Suggestion) { s.Reason = "a\rb" },
		"long":      func(s *Suggestion) { s.Command = strings.Repeat("x", 501) },
		"zerowidth": func(s *Suggestion) { s.Command = "rm​ x" },
	} {
		s := ok
		change(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	other := ok
	other.Command = "pnpm i"
	if ok.ID() == other.ID() {
		t.Error("ids must follow the content")
	}
}
