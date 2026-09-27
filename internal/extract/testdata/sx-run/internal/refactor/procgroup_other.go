//go:build !unix

package refactor

import "os/exec"

func ownGroup(cmd *exec.Cmd) {}

func killGroup(pid int) {}
