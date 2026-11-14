package tui

import (
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
)

// TestMain turns off cursor blinking: its timer commands would otherwise
// block the synchronous test driver on every keystroke.
func TestMain(m *testing.M) {
	inputCursorMode = cursor.CursorStatic
	os.Exit(m.Run())
}
