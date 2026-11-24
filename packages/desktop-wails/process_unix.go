//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func interruptProcess(process *os.Process) error {
	return process.Signal(syscall.SIGTERM)
}

func hideConsoleWindow(_ *exec.Cmd) {}
