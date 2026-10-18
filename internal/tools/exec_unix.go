//go:build unix

package tools

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group so a timeout can
// terminate children too.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
