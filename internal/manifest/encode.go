package manifest

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Encode renders the manifest as TOML with a stable key order and readable
// multi-line strings for purpose and context, matching the spec's example.
// It is hand-written because a generic encoder cannot keep that layout.
func Encode(m *Manifest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "schema  = %d\n", m.Schema)
	fmt.Fprintf(&b, "name    = %s\n", quote(m.Name))
	fmt.Fprintf(&b, "created = %s\n", m.Created.Format(time.RFC3339))
	if m.Harness != "" {
		fmt.Fprintf(&b, "harness = %s\n", quote(m.Harness))
	}
	writeText(&b, "purpose", m.Purpose)
	writeText(&b, "context", m.Context)
	writeInit(&b, m.Init)
	for _, mem := range m.Members {
		b.WriteString("\n[[member]]\n")
		fmt.Fprintf(&b, "name   = %s\n", quote(mem.Name))
		fmt.Fprintf(&b, "source = %s\n", quote(mem.Source))
		if mem.Remote != "" {
			fmt.Fprintf(&b, "remote = %s\n", quote(mem.Remote))
		}
		fmt.Fprintf(&b, "mode   = %s\n", quote(mem.Mode))
		fmt.Fprintf(&b, "path   = %s\n", quote(mem.Path))
		if mem.Mode == ModeActive && mem.Base != "" {
			fmt.Fprintf(&b, "base   = %s\n", quote(mem.Base))
		}
		if mem.Mode == ModeReference && mem.Ref != "" {
			fmt.Fprintf(&b, "ref    = %s\n", quote(mem.Ref))
		}
		if mem.Role != "" {
			fmt.Fprintf(&b, "role   = %s\n", quote(mem.Role))
		}
		if len(mem.Copy) > 0 {
			fmt.Fprintf(&b, "copy   = %s\n", quoteList(mem.Copy))
		}
		if mem.Hooks {
			b.WriteString("hooks  = true\n")
		}
		writeCommands(&b, "setup", mem.Setup)
		writeCommands(&b, "teardown", mem.Teardown)
	}
	for _, n := range m.Notes {
		b.WriteString("\n[[note]]\n")
		fmt.Fprintf(&b, "at   = %s\n", n.At.Format(time.RFC3339))
		fmt.Fprintf(&b, "by   = %s\n", quote(n.By))
		fmt.Fprintf(&b, "text = %s\n", quote(n.Text))
	}
	return []byte(b.String())
}

// writeCommands emits a commands key, as a plain string when there is one.
func writeCommands(b *strings.Builder, key string, cmds Commands) {
	if len(cmds) == 0 {
		return
	}
	parts := make([]string, len(cmds))
	for i, c := range cmds {
		parts[i] = quote(c)
	}
	val := parts[0]
	if len(parts) > 1 {
		val = "[" + strings.Join(parts, ", ") + "]"
	}
	fmt.Fprintf(b, "%-6s = %s\n", key, val)
}

// writeInit emits the [init] table. It is a table, so it follows every
// top-level key. Roles are written in name order to keep the file stable.
func writeInit(b *strings.Builder, i *Init) {
	if i == nil {
		return
	}
	b.WriteString("\n[init]\n")
	fmt.Fprintf(b, "at      = %s\n", i.At.Format(time.RFC3339))
	fmt.Fprintf(b, "by      = %s\n", quote(i.By))
	fmt.Fprintf(b, "members = %s\n", quoteList(i.Members))
	writeText(b, "overview", i.Overview)
	writeText(b, "purpose", i.Purpose)
	if len(i.Roles) > 0 {
		b.WriteString("\n[init.roles]\n")
		for _, name := range slices.Sorted(maps.Keys(i.Roles)) {
			fmt.Fprintf(b, "%s = %s\n", name, quote(i.Roles[name]))
		}
	}
}

// quoteList renders a TOML array of strings.
func quoteList(list []string) string {
	parts := make([]string, len(list))
	for i, s := range list {
		parts[i] = quote(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// writeText emits a string key, using a multi-line literal block
// when the value has newlines.
func writeText(b *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	if !strings.Contains(val, "\n") {
		fmt.Fprintf(b, "\n%s = %s\n", key, quote(val))
		return
	}
	// A multi-line basic string; escape what the TOML spec requires.
	esc := strings.ReplaceAll(val, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"""`, `""\"`)
	esc = strings.ReplaceAll(esc, "\r", `\r`)
	if !strings.HasSuffix(esc, "\n") {
		esc += "\n"
	}
	fmt.Fprintf(b, "\n%s = \"\"\"\n%s\"\"\"\n", key, esc)
}

// quote renders a single-line TOML basic string. strconv.Quote is not used
// because it emits escapes (\a, \v, \x..) that TOML does not define.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
