//go:build windows

package main

// Native Windows clipboard support for FILES (CF_HDROP) and IMAGES (CF_DIB).
// This is what makes copy-paste of files/screenshots work PC <-> PC:
// Explorer copies put a file list on the clipboard, screenshots put a DIB.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cfBitmap     = 2
	cfDIB        = 8
	cfHDROP      = 15
	cfDIBV5      = 17
	gmemMoveable = 0x0002
	gmemZeroInit = 0x0040
)

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")

	procOpenClipboard          = modUser32.NewProc("OpenClipboard")
	procCloseClipboard         = modUser32.NewProc("CloseClipboard")
	procEmptyClipboard         = modUser32.NewProc("EmptyClipboard")
	procGetClipboardData       = modUser32.NewProc("GetClipboardData")
	procSetClipboardData       = modUser32.NewProc("SetClipboardData")
	procIsClipboardFormatAvail = modUser32.NewProc("IsClipboardFormatAvailable")
	procGlobalLock             = modKernel32.NewProc("GlobalLock")
	procGlobalUnlock           = modKernel32.NewProc("GlobalUnlock")
	procGlobalSize             = modKernel32.NewProc("GlobalSize")
	procGlobalAlloc            = modKernel32.NewProc("GlobalAlloc")
	procGlobalFree             = modKernel32.NewProc("GlobalFree")
	procDragQueryFileW         = modShell32.NewProc("DragQueryFileW")
)

func openClipboard() bool {
	r, _, _ := procOpenClipboard.Call(0)
	return r != 0
}

// NOTE: memory is read/written with Read/WriteProcessMemory on our own
// process instead of unsafe.Pointer casts, so `go vet` stays clean.

func globalReadBytes(h, addr uintptr) []byte {
	size, _, _ := procGlobalSize.Call(h)
	if size == 0 || addr == 0 {
		return nil
	}
	buf := make([]byte, int(size))
	cur, _ := windows.GetCurrentProcess()
	var read uintptr
	if err := windows.ReadProcessMemory(cur, addr, &buf[0], size, &read); err != nil || read != size {
		return nil
	}
	return buf
}

// globalWriteAll allocates moveable global memory and fills it with data.
// On success the OS takes ownership after SetClipboardData.
func globalWriteAll(data []byte) (uintptr, bool) {
	if len(data) == 0 {
		return 0, false
	}
	h, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroInit), uintptr(len(data)))
	if h == 0 {
		return 0, false
	}
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		procGlobalFree.Call(h)
		return 0, false
	}
	cur, _ := windows.GetCurrentProcess()
	var written uintptr
	err := windows.WriteProcessMemory(cur, addr, &data[0], uintptr(len(data)), &written)
	procGlobalUnlock.Call(h)
	if err != nil || written != uintptr(len(data)) {
		procGlobalFree.Call(h)
		return 0, false
	}
	return h, true
}

func closeClipboard() {
	procCloseClipboard.Call()
}

func isFormatAvailable(f uint32) bool {
	r, _, _ := procIsClipboardFormatAvail.Call(uintptr(f))
	return r != 0
}

// ---------- read: file list ----------

func getClipboardFiles() []string {
	if !isFormatAvailable(cfHDROP) {
		return nil
	}
	if !openClipboard() {
		return nil
	}
	defer closeClipboard()
	h, _, _ := procGetClipboardData.Call(uintptr(cfHDROP))
	if h == 0 {
		return nil
	}
	count, _, _ := procDragQueryFileW.Call(h, 0xFFFFFFFF, 0, 0)
	var out []string
	for i := uint32(0); i < uint32(count); i++ {
		n, _, _ := procDragQueryFileW.Call(h, uintptr(i), 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		procDragQueryFileW.Call(h, uintptr(i), uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
		out = append(out, windows.UTF16ToString(buf))
	}
	return out
}

// ---------- read: image as PNG ----------

func getClipboardImagePNG() []byte {
	var dib []byte
	if isFormatAvailable(cfDIB) {
		dib = getClipboardBytes(cfDIB)
	} else if isFormatAvailable(cfDIBV5) {
		dib = getClipboardBytes(cfDIBV5)
	} else if isFormatAvailable(cfBitmap) {
		// CF_BITMAP handle needs GetDIBits conversion — skip, DIB covers
		// screenshots/snipping tool in practice.
		return nil
	} else {
		return nil
	}
	if len(dib) == 0 {
		return nil
	}
	return dibToPNG(dib)
}

func getClipboardBytes(format uint32) []byte {
	if !openClipboard() {
		return nil
	}
	defer closeClipboard()
	h, _, _ := procGetClipboardData.Call(uintptr(format))
	if h == 0 {
		return nil
	}
	addr, _, _ := procGlobalLock.Call(h)
	if addr == 0 {
		return nil
	}
	defer procGlobalUnlock.Call(h)
	return globalReadBytes(h, addr)
}

// dibToPNG converts a clipboard DIB (info header + pixels, no file header)
// with 24/32-bit depth to PNG bytes.
func dibToPNG(dib []byte) []byte {
	if len(dib) < 40 {
		return nil
	}
	hdrSize := binary.LittleEndian.Uint32(dib[0:4])
	if hdrSize != 40 && hdrSize != 108 && hdrSize != 124 {
		return nil
	}
	w := int(int32(binary.LittleEndian.Uint32(dib[4:8])))
	h := int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	bpp := binary.LittleEndian.Uint16(dib[14:16])
	topDown := false
	if h < 0 {
		h = -h
		topDown = true
	}
	if w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return nil
	}
	if bpp != 32 && bpp != 24 {
		return nil
	}
	stride := ((w*int(bpp) + 31) / 32) * 4
	off := int(hdrSize)
	if len(dib) < off+stride*h {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := y
		if !topDown {
			sy = h - 1 - y
		}
		row := dib[off+sy*stride:]
		for x := 0; x < w; x++ {
			if bpp == 32 {
				o := x * 4
				img.SetRGBA(x, y, color.RGBA{row[o+2], row[o+1], row[o], 0xFF})
			} else {
				o := x * 3
				img.SetRGBA(x, y, color.RGBA{row[o+2], row[o+1], row[o], 0xFF})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// ---------- write: file list (so received files are pastable) ----------

func nativeSetClipboardFiles(paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("no paths")
	}
	var units []uint16
	for _, p := range paths {
		u, err := windows.UTF16FromString(p)
		if err != nil {
			continue
		}
		units = append(units, u...) // UTF16FromString includes the null terminator
	}
	units = append(units, 0) // double-null terminator

	// DROPFILES header (20 bytes) + UTF-16 path list.
	raw := make([]byte, 20+len(units)*2)
	binary.LittleEndian.PutUint32(raw[0:4], 20) // pFiles offset
	raw[16] = 1                                 // fWide = TRUE
	for i, u := range units {
		binary.LittleEndian.PutUint16(raw[20+i*2:], u)
	}

	h, ok := globalWriteAll(raw)
	if !ok {
		return fmt.Errorf("GlobalAlloc failed")
	}

	if !openClipboard() {
		procGlobalFree.Call(h)
		return fmt.Errorf("OpenClipboard failed")
	}
	defer closeClipboard()
	procEmptyClipboard.Call()
	r, _, _ := procSetClipboardData.Call(uintptr(cfHDROP), h)
	if r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData failed")
	}
	return nil // OS owns the handle now
}

// ---------- write: PNG image ----------

func nativeSetClipboardImagePNG(pngBytes []byte) error {
	dib, err := pngToDIB(pngBytes)
	if err != nil {
		return err
	}
	h, ok := globalWriteAll(dib)
	if !ok {
		return fmt.Errorf("GlobalAlloc failed")
	}

	if !openClipboard() {
		procGlobalFree.Call(h)
		return fmt.Errorf("OpenClipboard failed")
	}
	defer closeClipboard()
	procEmptyClipboard.Call()
	r, _, _ := procSetClipboardData.Call(uintptr(cfDIB), h)
	if r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData failed")
	}
	return nil
}

func pngToDIB(pngBytes []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		// Maybe it's already raw bytes of another format — try generic decode.
		img2, _, err2 := image.Decode(bytes.NewReader(pngBytes))
		if err2 != nil {
			return nil, err
		}
		img = img2
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return nil, fmt.Errorf("bad image size %dx%d", w, h)
	}
	dib := make([]byte, 40+w*h*4)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], uint32(w))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(int32(-h))) // top-down
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := 40 + (y*w+x)*4
			dib[o] = byte(bl >> 8)
			dib[o+1] = byte(g >> 8)
			dib[o+2] = byte(r >> 8)
			dib[o+3] = 0
		}
	}
	return dib, nil
}

// ---------- poll integration ----------

// checkNativeFileCopy sends Explorer-copied files/folders (folders go as .zip).
// Returns true if the clipboard held files (handled or echo — skip text check).
func checkNativeFileCopy(ws *WSClient, state *ClipboardState, cfg *Config) bool {
	t0 := time.Now()
	paths := getClipboardFiles()
	if len(paths) == 0 {
		return false
	}
	fp := fileListFingerprint(paths)
	state.mu.Lock()
	echo := fp == state.lastSentFileFP || fp == state.lastAppliedFileFP
	state.mu.Unlock()
	if echo {
		return true
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			zb, err := zipDir(p)
			if err != nil {
				log.Printf("Could not zip %s: %v", p, err)
				continue
			}
			pushFileMsg(ws, cfg, zb, filepath.Base(p)+".zip", "application/zip", "file")
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		mime := http.DetectContentType(headBytes(b))
		pushFileMsg(ws, cfg, b, filepath.Base(p), mime, "file")
	}
	state.mu.Lock()
	state.lastSentFileFP = fp
	state.mu.Unlock()
	log.Printf("Clipboard files detected (%d file(s), detect=%v)", len(paths), time.Since(t0))
	return true
}

func headBytes(b []byte) []byte {
	if len(b) > 512 {
		return b[:512]
	}
	return b
}

// checkNativeImageCopy sends screenshots/pictures from the clipboard as PNG.
func checkNativeImageCopy(ws *WSClient, state *ClipboardState, cfg *Config) bool {
	pngBytes := getClipboardImagePNG()
	if len(pngBytes) == 0 {
		return false
	}
	hash := computeSha256(pngBytes)
	state.mu.Lock()
	echo := hash == state.lastSentHash || hash == state.lastAppliedHash
	state.mu.Unlock()
	if echo {
		return true
	}
	pushFileMsg(ws, cfg, pngBytes, "clipboard.png", "image/png", "image")
	state.mu.Lock()
	state.lastSentHash = hash
	state.mu.Unlock()
	return true
}

// archive/zip lives in watcher.go now (portable); Win32 clipboard below.
