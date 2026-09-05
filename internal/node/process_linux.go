package node

import (
	"os/exec"
	"syscall"
)

func childAttrs(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
