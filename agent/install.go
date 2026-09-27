package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// cliInstall makes `agent` runnable from any folder by adding its
// directory to the user's PATH.
func cliInstall() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("Cannot locate agent binary: %v\n", err)
		return 1
	}
	dir := filepath.Dir(exe)
	return installToPath(dir)
}
