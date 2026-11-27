package render

import "strings"

// AppSkillName is the directory name of the app skill. It differs from
// SkillName on purpose: a harness reads a user's global skills beside a
// session's own, and a global skill called "vmono" would shadow or collide
// with the one every session carries.
const AppSkillName = "vmono-app"

// appSkillDoc is one reference of the app skill: a file under references/
// that an agent reads only when its task calls for it.
type appSkillDoc struct {
	// name is the file's name without ".md", and what `vmono skill` takes.
	name string
	// when completes "read it when ..." in the skill's index.
	when string
	body string
}

// appSkillDocs are the app skill's references, in the order the index in
// SKILL.md lists them.
var appSkillDocs = []appSkillDoc{
	{"concepts", "you need to know what a package, session or member is, or where things live on disk", appConceptsDoc},
	{"setup", "vmono is missing, has not been set up, or does not know a repository", appSetupDoc},
	{"state", "the user asks what exists, what is in progress, or where some work stands", appStateDoc},
	{"packages", "defining a package, changing what it says or holds, or exploring it", appPackagesDoc},
	{"sessions", "starting a piece of work, building on a branch, or handing the work to the user", appSessionsDoc},
}

// AppSkillReferences returns the names of the app skill's references, in the
// order SKILL.md lists them.
func AppSkillReferences() []string {
	names := make([]string, len(appSkillDocs))
	for i, d := range appSkillDocs {
		names[i] = d.name
	}
	return names
}

// AppSkillReference returns the content of the named reference file, and false
// when the app skill has none by that name.
func AppSkillReference(name string) (string, bool) {
	for _, d := range appSkillDocs {
		if d.name == name {
			return "<!-- " + appSkillNote + " -->\n\n" + d.body, true
		}
	}
	return "", false
}

// AppSkillMarkdown returns the app skill's SKILL.md: what is needed on every
// use, followed by the index of references.
func AppSkillMarkdown() string {
	var b strings.Builder
	b.WriteString(appSkillHeader + appSkillIntro + appSkillCLI + appSkillRules + appSkillIndex)
	for _, d := range appSkillDocs {
		b.WriteString("- `references/" + d.name + ".md` (`vmono skill " + d.name + "`): read it when " + d.when + ".\n")
	}
	return b.String()
}

// AppSkillFiles returns every file of the app skill as a slash-separated path
// relative to the skill's directory mapped to its content. It is the complete
// contents of that directory.
func AppSkillFiles() map[string]string {
	files := map[string]string{"SKILL.md": AppSkillMarkdown()}
	for _, name := range AppSkillReferences() {
		files["references/"+name+".md"], _ = AppSkillReference(name)
	}
	return files
}
