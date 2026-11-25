package render

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/doneill612/vmono/internal/manifest"
)

// InitPrompt is the first prompt a harness gets when it initializes a package.
// It is one plain line so it survives a shell, a Windows command shim and
// herdr's line limit; the instructions themselves are in AGENTS.md, which
// every harness reads.
const InitPrompt = "Initialize this vmono package by following the task in AGENTS.md."

// codexRulesPath holds the Codex rules that let the agent read and record the
// package without asking.
const codexRulesPath = ".codex/rules/vmono.rules"

// initTask tells the agent what to explore and how to record it.
const initTask = `## Your task

You are initializing this package: explore its members, then record what every
future session of it needs to know. This is a read-only exploration. The
checkouts under ` + "`_ref/`" + ` are temporary and are removed when you exit, so do not
edit, build, commit or push anything.

1. Read each member: its README, its ` + "`AGENTS.md`" + ` / ` + "`CLAUDE.md`" + `, its manifests and
   build files, and enough of the code to say what it is for and how it is laid
   out. Read what is there rather than guessing from names.
2. Work out how the members relate: who calls, imports, deploys or documents
   whom, and which contracts (APIs, schemas, generated code) they share.
3. Record it, without asking first:

       vmono package record - <<'VMONO_EOF'
       {
         "purpose": "why these repositories belong together, in one to three sentences",
         "overview": "markdown, see below",
         "roles": {"MEMBER": "one line on what the member contributes"},
         "suggestions": [{"member": "MEMBER", "kind": "setup", "command": "one shell command", "reason": "the file that shows it is needed"}]
       }
       VMONO_EOF

   It is JSON: escape quotes and write newlines inside a string as \n. The
   command reports what it set, and what it kept because the user wrote it.

   Always send "suggestions", on a re-run too; send [] when no member needs any.
   Suggest a "setup" command when a fresh worktree of a member cannot be worked
   in without it (installing dependencies, as in npm install; generating code),
   and a "teardown" command for what must stop before a worktree goes away
   (containers). When one fits, send it rather than asking the user whether to:
   suggesting is how they are asked. Suggest only what the member's own files show, in the
   form they use, and nothing that deploys, publishes or needs credentials.
   Each is one line and runs in the member's directory. The commands members have
   already are listed under Members; one that is there, or that the user settled
   before, is dropped, so there is no need to check. Suggesting runs nothing: the
   user reviews each one after they exit. You cannot accept them, so do not try
   to. Sending "suggestions" again replaces the list; leave it out to keep it.
4. Show the user what you recorded and ask for corrections. Record again after
   each one; the new overview replaces the last, so always send the whole text.
   If the user wants to replace something that was kept, use
   ` + "`vmono describe --purpose`" + ` or ` + "`vmono role`" + `.
5. When the user is happy, tell them to exit; the temporary checkout goes away.

The overview is read at the start of every session, so keep it under about 60
lines. Cover what each member is and where its important parts live, how the
members connect, how each is built and tested, conventions that span them, and
gotchas. Use headings below ` + "`##`" + `, starting at ` + "`###`" + `. Leave out what the Context
already says, and anything easily seen from a file listing.

`

// InitBlock renders the vmono-owned block of AGENTS.md for an init workspace:
// the package as it stands and the task. m is the package as the workspace
// holds it, with every member a reference. A package that was initialized
// before also gets its overview and how the members changed since.
func InitBlock(m *manifest.Manifest) string {
	var b strings.Builder
	b.WriteString(BeginMarker + "\n<!-- " + GeneratedNote + " -->\n\n")
	fmt.Fprintf(&b, "# Initializing package: %s\n\n", m.Name)
	b.WriteString(initTask)

	section := func(title, text string) {
		if text = strings.TrimSpace(text); text != "" {
			b.WriteString("## " + title + "\n\n" + text + "\n\n")
		}
	}
	section("Purpose", m.Purpose)
	section("Context", m.Context)
	if o := m.Overview(); o != "" {
		section("Overview recorded earlier", o)
		added, removed := m.InitDrift()
		note := "Check it against the members and record a whole updated overview."
		if len(added) > 0 {
			note += " Members added since: " + strings.Join(added, ", ") + "."
		}
		if len(removed) > 0 {
			note += " Members removed since: " + strings.Join(removed, ", ") + "."
		}
		b.WriteString(note + "\n\n")
		suggestions := func(intro string, list []manifest.Suggestion) {
			if len(list) == 0 {
				return
			}
			b.WriteString(intro + "\n\n")
			for _, s := range list {
				fmt.Fprintf(&b, "- %s %s: `%s`\n", s.Member, s.Kind, s.Command)
			}
			b.WriteString("\n")
		}
		suggestions("Commands suggested earlier that still await the user's review:", m.Pending())
		suggestions("Commands the user already accepted or dismissed; do not suggest them again:", m.Init.Settled)
	}
	b.WriteString("## Members\n\n| Name | Path | Ref | Role |\n| --- | --- | --- | --- |\n")
	for _, mem := range m.Members {
		fmt.Fprintf(&b, "| %s | `%s/` | `%s` | %s |\n", mem.Name, path.Clean(mem.Path), mem.Ref, cell(mem.Role))
	}
	b.WriteString("\nSetup and teardown commands they have already:\n\n")
	none := true
	for _, mem := range m.Members {
		for _, cmd := range mem.Setup {
			none = false
			fmt.Fprintf(&b, "- %s setup: `%s`\n", mem.Name, oneLine(cmd))
		}
		for _, cmd := range mem.Teardown {
			none = false
			fmt.Fprintf(&b, "- %s teardown: `%s`\n", mem.Name, oneLine(cmd))
		}
	}
	if none {
		b.WriteString("- none\n")
	}
	b.WriteString("\n")
	if notes := m.RecentNotes(manifest.MaxNotesRendered); len(notes) > 0 {
		b.WriteString("## Notes\n\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "- %s (%s): %s\n", n.At.Format("2006-01-02 15:04"), n.By, oneLine(n.Text))
		}
		b.WriteString("\n")
	}
	b.WriteString(EndMarker + "\n")
	return b.String()
}

// RenderInit writes the files of an init workspace. Unlike a session's they
// leave out the vmono skill and plugin, whose guidance is about doing work,
// and members' hooks, skills and allow rules; members' ask and deny rules
// still apply.
func RenderInit(dir string, m *manifest.Manifest, opts Options) error {
	w := &writer{dir: dir}
	if err := w.file("AGENTS.md", InitBlock(m)); err != nil {
		return err
	}
	if err := w.file("CLAUDE.md", ClaudeMD); err != nil {
		return err
	}
	if err := w.file(".gitignore", Gitignore(m)); err != nil {
		return err
	}
	if opts.has(AdapterClaudeCode) {
		if err := renderClaudeInit(w, m); err != nil {
			return fmt.Errorf("claude-code adapter: %w", err)
		}
	}
	if opts.has(AdapterOpenCode) {
		if err := renderOpenCodeInit(w, m, opts.OpenCodeV2); err != nil {
			return fmt.Errorf("opencode adapter: %w", err)
		}
	}
	if opts.has(AdapterAgentSkills) {
		rules := "# " + GeneratedNote + "\n" +
			"prefix_rule(pattern = [\"vmono\", \"show\"], decision = \"allow\")\n" +
			"prefix_rule(pattern = [\"vmono\", \"package\", \"record\"], decision = \"allow\")\n" +
			"prefix_rule(pattern = [\"vmono\", \"package\", \"suggestions\"], decision = \"forbidden\")\n"
		if err := w.file(codexRulesPath, rules); err != nil {
			return fmt.Errorf("agent-skills adapter: %w", err)
		}
	}
	return nil
}

// renderClaudeInit writes .claude/settings.json for an init workspace: no
// edits, commits or pushes, and recording without asking.
func renderClaudeInit(w *writer, m *manifest.Manifest) error {
	rules := newRuleSet()
	for _, r := range []string{"Edit", "Write", "NotebookEdit", "Bash(git commit:*)", "Bash(git push:*)", "Bash(vmono package suggestions:*)"} {
		rules.add(r, EffectDeny)
	}
	for _, r := range []string{"Bash(vmono package record:*)", "Bash(vmono show:*)"} {
		rules.add(r, EffectAllow)
	}
	for _, mem := range m.Members {
		if cfg, ok := readClaudeMember(filepath.Join(w.dir, filepath.FromSlash(mem.Path))); ok {
			addClaudeRules(rules, mem.Path, cfg, EffectAsk, EffectDeny)
		}
	}
	data, err := json.MarshalIndent(claudeFile{Permissions: claudePermissions{
		Allow: rules.list(EffectAllow), Ask: rules.list(EffectAsk), Deny: rules.list(EffectDeny),
	}}, "", "  ")
	if err != nil {
		return err
	}
	return w.file(claudeSettingsPath, string(data)+"\n")
}

// renderOpenCodeInit writes opencode.json for an init workspace. The last
// matching rule wins, so the general rules come first, members' stricter ones
// next, and what the task needs last. Members' edit rules are left out: every
// edit is denied already, and a later ask would loosen that.
func renderOpenCodeInit(w *writer, m *manifest.Manifest, v2 bool) error {
	rules := &ocRules{}
	rules.add(ocRule{"edit", "*", EffectDeny}, false)
	rules.add(ocRule{"bash", "*", EffectAsk}, false)
	for _, mem := range m.Members {
		for _, r := range readOpenCodeMember(filepath.Join(w.dir, filepath.FromSlash(mem.Path))) {
			if r.Action == "edit" || r.Effect == EffectAllow {
				continue
			}
			if res, keep := RewriteOpenCodeResource(mem.Path, r.Action, r.Resource, r.Effect); keep {
				rules.add(ocRule{Action: r.Action, Resource: res, Effect: r.Effect}, false)
			}
		}
	}
	for _, r := range []ocRule{
		{"bash", "git commit *", EffectDeny}, {"bash", "git push *", EffectDeny},
		{"bash", "vmono show *", EffectAllow}, {"bash", "vmono package record *", EffectAllow},
		{"bash", "vmono package suggestions*", EffectDeny},
	} {
		rules.add(r, true)
	}
	doc := openCodeV1Doc(rules)
	if v2 {
		doc = openCodeV2Doc(rules)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return w.file(openCodeConfigPath, string(data)+"\n")
}
