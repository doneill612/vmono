package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/doneill612/vmono/internal/render"
	"github.com/doneill612/vmono/internal/testutil"
)

func TestSkillPrintsWhatTheBinaryCarries(t *testing.T) {
	testutil.NewEnv(t)
	ref, _ := render.AppSkillReference("sessions")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"skill"}, render.AppSkillMarkdown()},
		{[]string{"skill", "sessions"}, ref},
		{[]string{"skill", "--session"}, render.SkillMarkdown()},
		{[]string{"skill", "--list"}, strings.Join(render.AppSkillReferences(), "\n") + "\n"},
	} {
		out, stderr, err := run(t, c.args...)
		if err != nil || out != c.want {
			t.Errorf("%v = %v %s\n%.200s", c.args, err, stderr, out)
		}
	}
}

func TestSkillRefusesWhatItCannotPrint(t *testing.T) {
	testutil.NewEnv(t)
	_, _, err := run(t, "skill", "nope")
	if ExitCode(err) != ExitNotFound || !strings.Contains(err.Error(), "sessions") {
		t.Errorf("unknown reference = %d %v", ExitCode(err), err)
	}
	for _, args := range [][]string{
		{"skill", "sessions", "--list"},
		{"skill", "sessions", "--session"},
		{"skill", "--list", "--session"},
	} {
		if _, _, err := run(t, args...); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}

// commandsIn returns the words after "vmono" of every command a skill file
// shows, in backticks or on a line of a code block.
func commandsIn(body string) [][]string {
	re := regexp.MustCompile("(?m)(?:`|^)vmono((?: [a-z][a-z-]*)+)")
	var found [][]string
	for i, part := range strings.Split(body, "```") {
		for _, m := range re.FindAllStringSubmatch(part, -1) {
			// Outside a code block, a line may start with the word in prose.
			if i%2 == 1 || strings.HasPrefix(m[0], "`") {
				found = append(found, strings.Fields(m[1]))
			}
		}
	}
	return found
}

// TestSkillNamesOnlyRealCommands pins that every command either skill file
// names is one this binary has, so that a renamed command cannot leave the app
// skill behind.
func TestSkillNamesOnlyRealCommands(t *testing.T) {
	root := NewRoot(nil)
	root.InitDefaultHelpCmd()
	files := render.AppSkillFiles()
	files["the session skill"] = render.SkillMarkdown()
	checked := 0
	for path, body := range files {
		for _, words := range commandsIn(body) {
			checked++
			cmd, rest, err := root.Find(words)
			if err != nil || cmd == root || (cmd.HasSubCommands() && len(rest) > 0) {
				t.Errorf("%s names `vmono %s`, which is not a command", path, strings.Join(words, " "))
			}
		}
	}
	if checked < 20 {
		t.Errorf("only %d commands found in the skills", checked)
	}
}

func TestRootHelpPointsAgentsAtTheSkill(t *testing.T) {
	testutil.NewEnv(t)
	out, _, err := run(t, "--help")
	if err != nil || !strings.Contains(out, agentHelp) {
		t.Errorf("help = %v\n%s", err, out)
	}
}
