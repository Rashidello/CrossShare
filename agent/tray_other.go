//go:build !windows

package main

import "os/exec"

func hideConsole(cmd *exec.Cmd) {
	// No console to hide on other platforms.
}
