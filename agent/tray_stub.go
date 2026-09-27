//go:build !windows && !linux

package main

import "fmt"

// No systray toolchain for this OS — the tray is Windows/Linux only.
func runTray() {
	fmt.Println("The system tray is only available on Windows and Linux.")
	fmt.Println("Run 'agent' normally instead — it syncs in the foreground.")
}
