package main

import (
	"os/exec"
)

type cmdWrapper struct {
	*exec.Cmd
}

func newCmd(name string, args ...string) *cmdWrapper {
	return &cmdWrapper{Cmd: exec.Command(name, args...)}
}
