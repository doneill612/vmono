package render

import (
	"encoding/json"
	"strings"
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
	hooks, err := json.MarshalIndent(claudePluginHooks(), "", "  ")
	if err != nil {
		return nil, err
	}
	files := map[string]string{
		".claude-plugin/plugin.json": string(manifest) + "\n",
		"SKILL.md":                   SkillMarkdown(),
		"hooks/hooks.json":           string(hooks) + "\n",
	}
	for _, c := range claudeCommands {
		files["skills/"+c.name+"/SKILL.md"] = c.markdown()
	}
	return files, nil
}

// claudePluginHooks is the plugin's hooks/hooks.json: one SessionStart hook
// that runs `vmono hook session-start`, whose output Claude Code adds to the
// conversation as context. With no matcher it runs for every start source:
// a new conversation, a resume, a clear, a fork, and after compaction, which
// is when the live state is most likely to have been lost.
//
// It is in exec form (command plus args, no shell), so it behaves the same
// on Windows without Git Bash. It relies on vmono being on PATH, as the
// skill already does for every command it teaches. The timeout bounds the
// git status calls so a slow repo cannot hold up the start of a session.
func claudePluginHooks() map[string]any {
	return map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": "vmono",
							"args":    []string{"hook", "session-start"},
							"timeout": 20,
						},
					},
				},
			},
		},
	}
}

// claudeCommand is one of the plugin's skills that the user invokes by name,
// as /vmono:<name>. Each is a thin entry point that quotes guidance the root
// skill already carries, so there is one wording of it to maintain.
type claudeCommand struct {
	name        string
	description string
	// argumentHint is shown after the command name; empty for none.
	argumentHint string
	// allowedTools are the tool rules the command may use without asking.
	// The finish command has none: the adapter's `ask` rule for
	// `vmono session finish` must keep prompting.
	allowedTools []string
	// userOnly stops Claude from running the command on its own, for the
	// commands that act on the user's say-so.
	userOnly bool
	body     string
}

// markdown renders the command as a SKILL.md: YAML frontmatter, the
// generated-file note, then the body. Every string value is written as a
// JSON string, which is also a valid YAML double-quoted scalar, so colons
// and asterisks in descriptions and tool rules need no escaping care.
func (c claudeCommand) markdown() string {
	quote := func(v string) string {
		out, _ := json.Marshal(v)
		return string(out)
	}
	var b strings.Builder
	b.WriteString("---\nname: " + c.name + "\n")
	b.WriteString("description: " + quote(c.description) + "\n")
	if c.argumentHint != "" {
		b.WriteString("argument-hint: " + quote(c.argumentHint) + "\n")
	}
	if len(c.allowedTools) > 0 {
		b.WriteString("allowed-tools: " + quote(strings.Join(c.allowedTools, " ")) + "\n")
	}
	if c.userOnly {
		b.WriteString("disable-model-invocation: true\n")
	}
	b.WriteString("---\n<!-- " + GeneratedNote + " -->\n\n")
	b.WriteString(c.body)
	return b.String()
}

// claudeCommands are the plugin's /vmono:<name> commands, in the order they
// are documented.
var claudeCommands = []claudeCommand{
	{
		name:        "finish",
		description: "Land this vmono session's commits on each member's landing branch",
		userOnly:    true,
		body: `# Finish this vmono session

The user ran this command, so they have said the work is done. If you are not
inside a vmono session (see "Recognize a session" in the ` + "`vmono`" + ` skill), say so and
stop.

` + finishSection,
	},
	{
		name:         "note",
		description:  "Record a note on this vmono session's package, for every future session",
		argumentHint: "[note text]",
		allowedTools: []string{"Bash(vmono note:*)"},
		userOnly:     true,
		body: `# Record a package note

The note belongs to the package, not to this session: every session of it,
now and later, reads the same notes. If you are not inside a vmono session,
say so and stop.

The user's text, if any: $ARGUMENTS

If that is not empty, record it as given with ` + "`vmono note`" + `, quoting it safely
for the shell, then tell the user it was recorded and applies to every session.

If it is empty, look back over the conversation for what every future session
of this package will need, and propose one note. Show the user the text and
write it only once they agree. If nothing qualifies, say so rather than
inventing a note.

` + whatGoesWhere,
	},
	{
		name:         "status",
		description:  "Summarize this vmono session: each member's branch, uncommitted work and what is left to land",
		allowedTools: []string{"Bash(vmono status:*)"},
		body: `# Session status

Run ` + "`vmono status --json`" + ` and summarize it for the user in a few lines: the
session's ` + "`state`" + ` (in-progress or finished) and, for each member, its branch, any
uncommitted or untracked files, and how many commits are left to land on its
` + "`into`" + ` branch. Call out anything that would block ` + "`vmono session finish`" + `
(uncommitted work, a rebase in progress, a missing worktree).

If the command says you are not inside a vmono session, tell the user that.
This only reads state; change nothing.
`,
	},
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
