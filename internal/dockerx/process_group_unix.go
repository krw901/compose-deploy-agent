//go:build unix

package dockerx

import (
	"os/exec"
	"syscall"
)

// killProcessGroupOnCancel starts cmd in its own process group and makes
// context cancellation SIGKILL the entire group.
func killProcessGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
