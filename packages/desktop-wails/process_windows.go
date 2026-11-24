//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// Windows has no signal a console-less child can receive, so the server is
// ended directly. PocketBase writes through SQLite's journal, which survives
// this.
func interruptProcess(process *os.Process) error {
	return process.Kill()
}

// The server is a console program; without this flag each start would open
// a console window next to the app.
func hideConsoleWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow, HideWindow: true}
}
