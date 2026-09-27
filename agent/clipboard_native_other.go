//go:build !windows

package main

// Non-Windows builds: native file/image clipboard copy-paste is a
// Windows-first feature. Everything compiles and runs; only text sync
// plus the send-folder flow are active here.

import "fmt"

func getClipboardFiles() []string  { return nil }
func getClipboardImagePNG() []byte { return nil }
func nativeSetClipboardFiles(paths []string) error {
	return fmt.Errorf("file clipboard not supported on this OS")
}
func nativeSetClipboardImagePNG(pngBytes []byte) error {
	return fmt.Errorf("image clipboard not supported on this OS")
}

func checkNativeFileCopy(ws *WSClient, state *ClipboardState, cfg *Config) bool {
	return false
}

func checkNativeImageCopy(ws *WSClient, state *ClipboardState, cfg *Config) bool {
	return false
}
