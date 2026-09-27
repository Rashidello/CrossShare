//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func installToPath(dir string) int {
	link := "/usr/local/bin/agent"
	src := filepath.Join(dir, "agent")
	if err := os.Symlink(src, link); err == nil {
		fmt.Println("Linked into /usr/local/bin — `agent` works from any folder.")
		return 0
	}
	// No root: fall back to shell rc.
	line := fmt.Sprintf("\n# CrossShare agent\nexport PATH=\"$PATH:%s\"\n", dir)
	shell := os.Getenv("SHELL")
	rc := ""
	if strings.Contains(shell, "zsh") {
		rc = filepath.Join(os.Getenv("HOME"), ".zshrc")
	} else {
		rc = filepath.Join(os.Getenv("HOME"), ".bashrc")
	}
	if b, err := os.ReadFile(rc); err == nil && strings.Contains(string(b), dir) {
		fmt.Println("Already on PATH — `agent` works from any folder.")
		return 0
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Couldn't link (%v) or update %s.\nAdd this to your shell config:\n%s", err, rc, line)
		return 1
	}
	defer f.Close()
	f.WriteString(line)
	fmt.Printf("Added to %s. Restart your terminal, then run `agent status`.\n", rc)
	return 0
}
