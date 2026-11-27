// Command gen writes the app skill into the repository's skills directory,
// where tools that install skills from a repository, such as `npx skills`,
// find it. Run it with `go generate ./internal/render`.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/doneill612/vmono/internal/render"
)

// dest is the skill's directory relative to internal/render, which is where
// go generate runs this command.
var dest = filepath.Join("..", "..", "skills", render.AppSkillName)

// main replaces dest with exactly the files of the app skill, so that one the
// skill no longer has does not linger.
func main() {
	log.SetFlags(0)
	if err := os.RemoveAll(dest); err != nil {
		log.Fatal(err)
	}
	for rel, body := range render.AppSkillFiles() {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}
