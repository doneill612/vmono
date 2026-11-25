package tui

import (
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/launcher"
)

// TestConfirmSaysItClosesWhereYouAre covers deleting a session from the terminal
// it runs in, which goes along with it.
func TestConfirmSaysItClosesWhereYouAre(t *testing.T) {
	old := insideOf
	t.Cleanup(func() { insideOf = old })
	insideOf = func(pkg, session, dir string) string { return `herdr workspace "` + pkg + "/" + session + `"` }
	m := newConfirm(nil, "Delete session", "work", nil, true, nil, nil).about("session").closes("pay", "work", "/x")
	if v := m.View(100, 40); !strings.Contains(v, `This closes the herdr workspace "pay/work", which you are in.`) {
		t.Errorf("view:\n%s", v)
	}
	insideOf = launcher.InsideOf
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "")
	m = newConfirm(nil, "Delete session", "work", nil, true, nil, nil).closes("pay", "work", "/x")
	if len(m.closing) != 0 {
		t.Errorf("closing = %v", m.closing)
	}
}
