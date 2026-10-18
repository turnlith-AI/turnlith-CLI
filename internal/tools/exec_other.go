//go:build !unix

package tools

import "os/exec"

// setProcessGroup is a no-op on platforms without process groups.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessGroup kills only the direct child where process groups are
// unavailable.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
