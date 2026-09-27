package main

// Portable tray helpers (all platforms).

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func isTrayExe() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(filepath.Base(exe)), "tray")
}

func setupFileLogging(home string) {
	dir := filepath.Join(home, ".devdrop")
	os.MkdirAll(dir, 0755)
	f, err := os.OpenFile(filepath.Join(dir, "agent.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.Printf("--- log started ---")
}
