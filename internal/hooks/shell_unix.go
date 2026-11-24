//go:build !windows

package hooks

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand builds the command that runs line through sh, in a process
// group of its own that is killed as a whole when ctx ends.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd
}
