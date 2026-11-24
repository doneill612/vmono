//go:build windows

package hooks

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
)

// shellCommand builds the command that runs line through cmd.exe, killed with
// everything it started when ctx ends. The command line is passed as is: Go
// would otherwise quote it for programs that parse arguments the C runtime's
// way, which cmd.exe does not.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + line + `"`}
	cmd.Cancel = func() error {
		if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd
}
