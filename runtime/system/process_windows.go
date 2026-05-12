//go:build windows

package system

import "os/exec"

func configureCommandProcess(*exec.Cmd) {}

func killCommandProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
