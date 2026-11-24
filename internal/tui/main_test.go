package tui

import (
	"os"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"

	"github.com/doneill612/vmono/internal/testutil"
)

// TestMain turns off cursor blinking: its timer commands would otherwise
// block the synchronous test driver on every keystroke. The tests are spread over
// several processes.
func TestMain(m *testing.M) {
	inputCursorMode = cursor.CursorStatic
	previewDelay = 0
	os.Exit(testutil.Main(m))
}
