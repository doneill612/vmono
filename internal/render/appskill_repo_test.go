package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommittedAppSkillIsCurrent pins that skills/vmono-app, which is what
// gets installed from the repository, holds exactly what the binary prints.
func TestCommittedAppSkillIsCurrent(t *testing.T) {
	const fix = "run `go generate ./internal/render`"
	dir := filepath.Join("..", "..", "skills", AppSkillName)
	want := AppSkillFiles()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		body, ok := want[rel]
		if !ok {
			t.Errorf("%s is not part of the skill: %s", rel, fix)
			return nil
		}
		delete(want, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// A Windows checkout may have turned the line endings into CRLF.
		if strings.ReplaceAll(string(data), "\r\n", "\n") != body {
			t.Errorf("%s is out of date: %s", rel, fix)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%v: %s", err, fix)
	}
	for rel := range want {
		t.Errorf("%s is missing: %s", rel, fix)
	}
}

// TestRepositoryOffersOnlyTheAppSkill pins that the app skill is the only
// SKILL.md in the repository. Installers search a repository's harness
// directories as well as skills/, and the session skill must never be offered
// for a global install.
func TestRepositoryOffersOnlyTheAppSkill(t *testing.T) {
	root := filepath.Join("..", "..")
	only := filepath.Join(root, "skills", AppSkillName, "SKILL.md")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Neither holds files of this checkout: .claude/worktrees has others.
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "worktrees") {
			return filepath.SkipDir
		}
		if d.Name() == "SKILL.md" && path != only {
			t.Errorf("%s would be offered for install beside the app skill", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
