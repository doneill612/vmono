package core

import (
	"os"
	"testing"

	"github.com/doneill612/vmono/internal/testutil"
)

// TestMain spreads the tests over several processes.
func TestMain(m *testing.M) {
	os.Exit(testutil.Main(m))
}
