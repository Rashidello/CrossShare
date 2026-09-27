//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func installToPath(dir string) int {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Environment`, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		fmt.Printf("Cannot open registry: %v\n", err)
		return 1
	}
	defer k.Close()
	cur, _, err := k.GetStringValue("Path")
	if err != nil {
		cur = ""
	}
	for _, p := range strings.Split(cur, ";") {
		if strings.EqualFold(strings.TrimRight(p, `\`), strings.TrimRight(dir, `\`)) {
			fmt.Println("Already on PATH — `agent` works from any folder.")
			fmt.Println("If a terminal doesn't see it, open a NEW terminal window.")
			return 0
		}
	}
	next := cur
	if next != "" && !strings.HasSuffix(next, ";") {
		next += ";"
	}
	next += dir
	if err := k.SetStringValue("Path", next); err != nil {
		fmt.Printf("Cannot update PATH: %v\n", err)
		return 1
	}
	fmt.Println("Added to your user PATH. Open a NEW terminal and run `agent status`.")
	return 0
}
