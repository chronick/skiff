//go:build unix

package scheduler

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts cmd in its own process group and kills the whole group
// (negative PID) when the command's context is canceled, so children forked by
// wrapper commands die with the wrapper instead of running to completion.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
