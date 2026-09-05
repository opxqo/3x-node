//go:build !linux

package node

import "os/exec"

func childAttrs(_ *exec.Cmd) {}
