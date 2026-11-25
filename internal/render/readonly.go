package render

// readOnlyCommand is one vmono invocation that only reads or displays, so a
// session's permission rules allow it without asking.
type readOnlyCommand struct {
	// args is what follows `vmono`, such as "session ls".
	args string
	// exact is true when only this exact command line is allowed, with no
	// further arguments. It is how `doctor` is allowed without `--fix`, and
	// how a flag-only command such as `--version` is spelled.
	exact bool
}

// vmonoReadOnlyCommands are the vmono commands the generated rules allow, and
// that the two adapters share so they cannot drift apart. Every one of them
// changes no package, session or repository: they print state, and nothing
// else. `session review` is the read-only form of `session finish` (it plans
// the landing and moves nothing), which is a command of its own because a
// harness rule that asks for `session finish` would also ask for
// `session finish --dry-run`.
//
// Commands that can change state are deliberately absent, among them `doctor
// --fix`, `sync`, `session update`, `session into` with flags, `note`, `role`,
// `describe` and `add`. `doctor` is allowed without `--fix`: besides reading
// it rebuilds the index, which is only a cache.
var vmonoReadOnlyCommands = []readOnlyCommand{
	{args: "show"},
	{args: "status"},
	{args: "ls"},
	{args: "repos"},
	{args: "path"},
	{args: "inspect"},
	{args: "session ls"},
	{args: "session review"},
	{args: "integration ls"},
	{args: "integration print"},
	{args: "doctor", exact: true},
	{args: "doctor --json", exact: true},
	{args: "--version", exact: true},
}

// claudeRule spells the command as a Claude Code Bash rule: a prefix rule
// that also matches the command with arguments, or the exact command line.
func (c readOnlyCommand) claudeRule() string {
	if c.exact {
		return "Bash(vmono " + c.args + ")"
	}
	return "Bash(vmono " + c.args + ":*)"
}

// openCodeResource spells the command as an OpenCode bash pattern, where a
// trailing " *" also matches the command with arguments.
func (c readOnlyCommand) openCodeResource() string {
	if c.exact {
		return "vmono " + c.args
	}
	return "vmono " + c.args + " *"
}
