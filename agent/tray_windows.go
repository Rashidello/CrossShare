//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole makes the agent child run with no console window at all.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
