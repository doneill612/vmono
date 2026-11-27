package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/terms"
)

func TestHelpConceptsExplainsEveryTerm(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot(nil)
	root.SetOut(&out)
	root.SetArgs([]string{"help", "concepts"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, e := range terms.Entries {
		if !strings.Contains(out.String(), "\n\n"+e.Term+"\n  ") {
			t.Errorf("%q is not explained:\n%s", e.Term, out.String())
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if len(line) > conceptsWidth {
			t.Errorf("line is %d wide: %q", len(line), line)
		}
	}
}
