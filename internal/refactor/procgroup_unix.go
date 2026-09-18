//go:build unix

package refactor

import (
	"os/exec"
	"syscall"
)

// ownGroup puts cmd and everything it starts in a process group of its own,
// so an interrupted run can stop all of it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killGroup(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
}
