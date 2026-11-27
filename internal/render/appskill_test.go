package render

import (
	"regexp"
	"strings"
	"testing"
)

func TestAppSkillFrontmatter(t *testing.T) {
	body := AppSkillMarkdown()
	if !strings.HasPrefix(body, "---\nname: "+AppSkillName+"\ndescription: ") {
		t.Errorf("SKILL.md must open with its name and description:\n%.200s", body)
	}
	if AppSkillName == SkillName {
		t.Error("a global skill with the session skill's name would shadow it")
	}
}

// TestAppSkillIndexMatchesItsFiles pins that every reference SKILL.md names
// exists, and that none exists that an agent is never told about.
func TestAppSkillIndexMatchesItsFiles(t *testing.T) {
	files := AppSkillFiles()
	named := map[string]bool{}
	for _, path := range regexp.MustCompile(`references/[\w-]+\.md`).FindAllString(files["SKILL.md"], -1) {
		named[path] = true
		if files[path] == "" {
			t.Errorf("SKILL.md names %s, which does not exist", path)
		}
	}
	if len(files) != len(named)+1 {
		t.Errorf("SKILL.md names %d references, files holds %d", len(named), len(files)-1)
	}
	for _, name := range AppSkillReferences() {
		if !strings.Contains(files["SKILL.md"], "`vmono skill "+name+"`") {
			t.Errorf("SKILL.md does not say how to print %s", name)
		}
	}
	if _, ok := AppSkillReference("nope"); ok {
		t.Error("unknown reference was found")
	}
}

// TestAppSkillLeavesSyntaxToTheBinary pins that the skill names commands
// without their flags, which a vmono of another version may spell differently.
// The flags it may show are the binary's own way into help and the skills.
func TestAppSkillLeavesSyntaxToTheBinary(t *testing.T) {
	allowed := map[string]bool{"--help": true, "--version": true, "--session": true}
	flag := regexp.MustCompile(`--[a-z][a-z-]+`)
	for path, body := range AppSkillFiles() {
		for _, f := range flag.FindAllString(body, -1) {
			if !allowed[f] {
				t.Errorf("%s names the flag %s", path, f)
			}
		}
	}
}

func TestAppSkillTeachesItsLimits(t *testing.T) {
	var all strings.Builder
	for _, body := range AppSkillFiles() {
		all.WriteString(body)
	}
	for _, want := range []string{
		"VMONO_SESSION", "vmono <command> --help", "Never push", "never run it yourself",
		"`vmono open`", "A new session, not a new package",
	} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("skill missing %q", want)
		}
	}
}
