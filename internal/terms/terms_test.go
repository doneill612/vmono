package terms

import (
	"testing"

	"github.com/doneill612/vmono/internal/manifest"
)

func TestEntriesAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Entries {
		if e.Term == "" || e.Short == "" || e.Long == "" {
			t.Errorf("incomplete entry %+v", e)
		}
		if seen[e.Term] {
			t.Errorf("%q is defined twice", e.Term)
		}
		seen[e.Term] = true
		// The help dialog has room for this much beside the term.
		if len(e.Short) > 50 {
			t.Errorf("short form of %q is %d characters", e.Term, len(e.Short))
		}
	}
}

func TestModeLabel(t *testing.T) {
	for _, c := range []struct {
		mode string
		long bool
		want string
	}{
		{manifest.ModeActive, false, "active"},
		{manifest.ModeActive, true, "active"},
		{manifest.ModeReference, false, "read-only"},
		{manifest.ModeReference, true, "reference (read-only)"},
	} {
		if got := ModeLabel(c.mode, c.long); got != c.want {
			t.Errorf("ModeLabel(%q, %v) = %q, want %q", c.mode, c.long, got, c.want)
		}
	}
}
