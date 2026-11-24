package tui

import (
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/discover"
	"github.com/doneill612/vmono/internal/manifest"
)

// TestPickerShowsScanningAlongsideExistingMembers guards editing a package:
// its members are listed and checked straight away, and the scanning line
// must still show until the discovery scan reports back.
func TestPickerShowsScanningAlongsideExistingMembers(t *testing.T) {
	member := discover.Repo{Name: "alpha", Path: "/src/alpha"}
	p := newPicker(&env{}, map[string]string{member.Path: manifest.ModeActive}, []discover.Repo{member}, nil, nil)

	v := p.View(100, 20)
	if !strings.Contains(v, "alpha") || !strings.Contains(v, "[x]") {
		t.Errorf("existing member should be listed and selected:\n%s", v)
	}
	if !strings.Contains(v, "scanning") {
		t.Errorf("scanning indicator should show while the scan runs:\n%s", v)
	}

	p.loaded = true
	if v := p.View(100, 20); strings.Contains(v, "scanning") {
		t.Errorf("scanning indicator should clear once loaded:\n%s", v)
	}
}
