package render

import (
	"encoding/json"
)

// claudePluginDir is where the Claude Code plugin lives in a session. A
// folder under a project's .claude/skills/ that holds a .claude-plugin/
// plugin.json is a "skills-directory plugin": Claude Code loads it in place
// when the session starts, with no marketplace and no install step, once the
// user has trusted the session folder (id vmono@skills-dir).
//
// It is the same directory the plain bundled skill occupies for other
// harnesses, which is what makes the fallback seamless. The plugin's root
// SKILL.md is that skill, so a Claude Code that blocks plugins (an
// organization policy) or predates them still loads the skill and loses only
// the plugin's extras.
const claudePluginDir = ".claude/skills/" + SkillName

// claudePluginManifest is the plugin's .claude-plugin/plugin.json. The name
// must stay SkillName: the root skill is invoked as /vmono and the plugin's
// own skills as /vmono:<skill>. No version is set because a plugin loaded in
// place always reads its current files, whatever its version says.
type claudePluginManifest struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Author      claudePluginAuthor `json:"author"`
}

// claudePluginAuthor is the manifest's author object, whose name is
// required.
type claudePluginAuthor struct {
	Name string `json:"name"`
}

// claudePluginFiles returns every file of the Claude Code plugin as a path
// relative to claudePluginDir mapped to its content. The set is identical
// for every session, so nothing in it needs the manifest or the session.
// writeClaudePlugin treats it as the complete contents of the directory.
func claudePluginFiles() (map[string]string, error) {
	manifest, err := json.MarshalIndent(claudePluginManifest{
		Name:        SkillName,
		Description: "Work inside a vmono session: read its package, record what every future session needs, and land the work",
		Author:      claudePluginAuthor{Name: "vmono"},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]string{
		".claude-plugin/plugin.json": string(manifest) + "\n",
		"SKILL.md":                   SkillMarkdown(),
	}, nil
}

// writeClaudePlugin writes the Claude Code plugin into the session, and
// removes anything else under its directory so that a file dropped from a
// later version of vmono does not linger and keep loading.
func writeClaudePlugin(w *writer) error {
	files, err := claudePluginFiles()
	if err != nil {
		return err
	}
	return w.tree(claudePluginDir, files)
}
