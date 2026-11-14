package render

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/doneill612/vmono/internal/manifest"
)

// Effect is a permission rule's outcome, ordered by strictness.
type Effect int

// Effects from least to most strict. When two sources disagree about the
// same rule, the stricter one wins.
const (
	EffectAllow Effect = iota + 1
	EffectAsk
	EffectDeny
)

// String returns the lowercase name used in config files.
func (e Effect) String() string {
	switch e {
	case EffectAllow:
		return "allow"
	case EffectAsk:
		return "ask"
	case EffectDeny:
		return "deny"
	}
	return ""
}

// ParseEffect converts "allow", "ask" or "deny" to an Effect, or 0.
func ParseEffect(s string) Effect {
	switch s {
	case "allow":
		return EffectAllow
	case "ask":
		return EffectAsk
	case "deny":
		return EffectDeny
	}
	return 0
}

// ruleSet is an insertion-ordered set of rules keyed by their text, where a
// repeated rule keeps the strictest effect.
type ruleSet struct {
	order   []string
	effects map[string]Effect
}

// newRuleSet creates an empty set.
func newRuleSet() *ruleSet { return &ruleSet{effects: map[string]Effect{}} }

// add inserts a rule, upgrading its effect if a stricter one is added.
func (s *ruleSet) add(rule string, e Effect) {
	cur, ok := s.effects[rule]
	if !ok {
		s.order = append(s.order, rule)
	}
	if e > cur {
		s.effects[rule] = e
	}
}

// list returns the rules with the given effect in insertion order, never
// nil so JSON encodes an empty array.
func (s *ruleSet) list(e Effect) []string {
	out := []string{}
	for _, r := range s.order {
		if s.effects[r] == e {
			out = append(out, r)
		}
	}
	return out
}

// pathTools are the tools whose rule argument is a file path pattern.
var pathTools = map[string]bool{
	"Read": true, "Edit": true, "Write": true, "MultiEdit": true,
	"NotebookEdit": true, "Glob": true, "Grep": true,
}

// splitRule splits "Tool(arg)" into its parts. A bare "Tool" has no arg.
func splitRule(rule string) (tool, arg string, hasArg bool) {
	i := strings.IndexByte(rule, '(')
	if i < 0 || !strings.HasSuffix(rule, ")") {
		return rule, "", false
	}
	return rule[:i], rule[i+1 : len(rule)-1], true
}

// RewritePathPattern re-anchors a member's path pattern under the member's
// folder at the package root, using Claude Code's "/" project anchor.
//
// Absolute (//) and home (~/) patterns are untouched. Patterns with a
// directory part ("src/**", "./src", "/src") become "/<member>/src/**".
// Bare names and single-segment globs (".env", "*.env"), which gitignore
// semantics match at any depth, become "/<member>/**/.env" so they keep
// matching at any depth inside the member only.
func RewritePathPattern(memberPath, pattern string) string {
	if strings.HasPrefix(pattern, "//") || strings.HasPrefix(pattern, "~/") {
		return pattern
	}
	memberPath = path.Clean(strings.ReplaceAll(memberPath, `\`, "/"))
	rest := strings.TrimPrefix(strings.TrimPrefix(pattern, "./"), "/")
	switch {
	case rest == "" || rest == "**":
		return "/" + memberPath + "/**"
	case !strings.Contains(rest, "/"):
		return "/" + memberPath + "/**/" + rest
	default:
		return "/" + memberPath + "/" + rest
	}
}

// RewriteRule rewrites one Claude Code permission rule from a member's
// settings so it applies inside that member only. It returns ok=false for a
// rule that cannot be scoped safely: a bare path-tool allow such as "Edit"
// would grant access across the whole package.
func RewriteRule(memberPath, rule string, e Effect) (string, bool) {
	tool, arg, hasArg := splitRule(rule)
	if !pathTools[tool] {
		return rule, true
	}
	if !hasArg || arg == "" || arg == "*" {
		if e == EffectAllow {
			return "", false
		}
		return tool + "(" + RewritePathPattern(memberPath, "**") + ")", true
	}
	return tool + "(" + RewritePathPattern(memberPath, arg) + ")", true
}

// skillMarker marks a copied skill directory as vmono-generated so stale
// copies can be removed without touching the user's own skills.
const skillMarker = ".vmono-member"

var frontNameRE = regexp.MustCompile(`(?m)^name:[ \t]*.*$`)

// RewriteSkillName sets the frontmatter name of a SKILL.md to newName. Text
// without frontmatter is returned unchanged.
func RewriteSkillName(content, newName string) string {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return content
	}
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return content
	}
	front := content[:3+end]
	rest := content[3+end:]
	return frontNameRE.ReplaceAllString(front, "name: "+newName) + rest
}

// skillRoot describes where one adapter keeps project skills at the package
// root and where it looks for them inside a member.
type skillRoot struct {
	dest    string   // e.g. ".claude/skills"
	sources []string // member-relative skill dirs
}

// syncMemberSkills copies each member's project skills to the package root
// as <dest>/<member>-<skill>/ and removes stale copies from earlier syncs.
// Copies are plain files, never symlinks, and are refreshed on every call.
func syncMemberSkills(w *writer, m *manifest.Manifest, root skillRoot) error {
	desired := map[string]bool{}
	for _, mem := range m.Members {
		memberDir := filepath.Join(w.dir, filepath.FromSlash(mem.Path))
		for _, src := range root.sources {
			skillsDir := filepath.Join(memberDir, filepath.FromSlash(src))
			entries, err := os.ReadDir(skillsDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
					continue
				}
				destName := mem.Name + "-" + e.Name()
				if err := copySkill(w, filepath.Join(skillsDir, e.Name()), root.dest+"/"+destName, destName, mem.Name); err != nil {
					return err
				}
				desired[destName] = true
			}
		}
	}
	return removeStaleSkills(w, root.dest, desired)
}

// copySkill copies one skill directory, renaming its frontmatter to match
// the new directory and stamping the marker file.
func copySkill(w *writer, srcDir, destRel, destName, member string) error {
	err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		content := string(data)
		if rel == "SKILL.md" {
			content = RewriteSkillName(content, destName)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return w.fileMode(destRel+"/"+filepath.ToSlash(rel), content, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	return w.file(destRel+"/"+skillMarker, member+"\n")
}

// removeStaleSkills deletes marked skill directories no longer wanted.
func removeStaleSkills(w *writer, destRel string, desired map[string]bool) error {
	entries, err := os.ReadDir(w.abs(destRel))
	if err != nil {
		return nil
	}
	var stale []string
	for _, e := range entries {
		if !e.IsDir() || desired[e.Name()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(w.abs(destRel), e.Name(), skillMarker)); err == nil {
			stale = append(stale, e.Name())
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		if err := w.remove(destRel + "/" + name); err != nil {
			return err
		}
	}
	return nil
}
