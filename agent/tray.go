//go:build windows || linux

package main

// CrossShare tray manager (Windows-first, works anywhere systray builds).
// Two ways to run it:
//   tray.exe        GUI binary built with -H windowsgui: NO console window.
//   agent tray      Same tray loop, but keeps the console (for debugging).
//
// The tray starts agent.exe as a HIDDEN child, watches it, and restarts it
// if it ever dies silently. Menu: status, open folders, restart, quit.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/getlantern/systray"
)

type trayState struct {
	cfg      *Config
	agentBin string
	child    *exec.Cmd
	quitting bool
	status   *systray.MenuItem
}

func runTray() {
	home, _ := os.UserHomeDir()
	setupFileLogging(home)
	defaultConfig := filepath.Join(home, ".devdrop", "config.json")
	cfg, err := loadConfig(defaultConfig)
	if err != nil {
		log.Printf("tray: config error: %v", err)
		return
	}
	st := &trayState{cfg: cfg, agentBin: findAgentBin()}
	systray.Run(func() { st.onReady() }, func() { st.onExit() })
}

func findAgentBin() string {
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "agent.exe")
		if runtime.GOOS != "windows" {
			cand = filepath.Join(filepath.Dir(exe), "agent")
		}
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if p, err := exec.LookPath("agent"); err == nil {
		return p
	}
	if p, err := exec.LookPath("agent.exe"); err == nil {
		return p
	}
	return ""
}

func (st *trayState) onReady() {
	systray.SetIcon(makeTrayIcon())
	systray.SetTitle("CrossShare")
	systray.SetTooltip("CrossShare — clipboard & file sync")

	st.status = systray.AddMenuItem("Starting…", "Connection status")
	st.status.Disable()
	mRefresh := systray.AddMenuItem("Refresh status", "Ask the agent for status")
	systray.AddSeparator()
	mSendDir := systray.AddMenuItem("Open send folder", "Where you drop files to share")
	mRecvDir := systray.AddMenuItem("Open received folder", "Where shared files land")
	mRestart := systray.AddMenuItem("Restart agent", "Stop and start the sync agent")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Stop syncing and close CrossShare")

	st.startChild()
	go st.watchdog()
	st.refreshStatus()

	go func() {
		for {
			select {
			case <-mRefresh.ClickedCh:
				st.refreshStatus()
			case <-mSendDir.ClickedCh:
				openFolder(st.cfg.SendDir)
			case <-mRecvDir.ClickedCh:
				openFolder(st.cfg.ReceivedDir)
			case <-mRestart.ClickedCh:
				st.status.SetTitle("Restarting…")
				st.stopChild()
				time.Sleep(time.Second)
				st.startChild()
				st.refreshStatus()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func (st *trayState) onExit() {
	st.quitting = true
	st.stopChild()
}

func (st *trayState) startChild() {
	if st.agentBin == "" {
		st.status.SetTitle("agent.exe not found next to tray.exe")
		return
	}
	cmd := exec.Command(st.agentBin)
	hideConsole(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("tray: failed to start agent: %v", err)
		st.status.SetTitle("Could not start agent")
		return
	}
	st.child = cmd
	log.Printf("tray: agent started (pid %d)", cmd.Process.Pid)
	go func() {
		cmd.Wait() // releases the process; watchdog decides whether to restart
	}()
}

func (st *trayState) stopChild() {
	if st.child != nil && st.child.Process != nil {
		st.child.Process.Kill()
		st.child.Wait()
		st.child = nil
	}
}

// watchdog restarts the agent if it dies on its own (the "closed by itself"
// case) — with the file log we can later see WHY it died. If it keeps dying
// within seconds (e.g. device was revoked), it slows to one retry a minute
// instead of hammering the server.
func (st *trayState) watchdog() {
	var starts []time.Time
	for !st.quitting {
		time.Sleep(5 * time.Second)
		if st.quitting {
			return
		}
		if st.child == nil || st.child.ProcessState != nil {
			now := time.Now()
			recent := []time.Time{now}
			for _, t := range starts {
				if now.Sub(t) < time.Minute {
					recent = append(recent, t)
				}
			}
			starts = recent
			if len(starts) > 3 {
				st.status.SetTitle("Stopped: device disconnected? Run 'agent login'")
				time.Sleep(time.Minute)
				continue
			}
			log.Printf("tray: agent gone, restarting…")
			st.startChild()
			st.refreshStatus()
		}
	}
}

func (st *trayState) refreshStatus() {
	c := newCLIClient(st.cfg)
	var s struct {
		Connected  bool   `json:"connected"`
		DeviceName string `json:"device_name"`
		ServerURL  string `json:"server_url"`
	}
	if err := c.get("/api/status", &s); err != nil {
		st.status.SetTitle("Agent not responding yet…")
		return
	}
	state := "offline"
	if s.Connected {
		state = "connected"
	}
	title := fmt.Sprintf("%s • %s", s.DeviceName, state)
	st.status.SetTitle(title)
	systray.SetTooltip("CrossShare — " + title)
}

func openFolder(dir string) {
	os.MkdirAll(dir, 0755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("tray: open folder failed: %v", err)
	}
}

// makeTrayIcon draws a small blue "share" glyph at runtime so the repo
// needs no binary icon asset.
func makeTrayIcon() []byte {
	const size = 64
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	blue := color.RGBA{0x25, 0x63, 0xEB, 0xFF}
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	draw.Draw(img, img.Bounds(), &image.Uniform{blue}, image.Point{}, draw.Src)
	// rounded corners (transparent)
	corner := func(cx, cy int) {
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				dx, dy := float64(16-cx-x-1), float64(16-cy-y-1)
				_ = dy
				if dx*dx+dy*dy > 15*15 {
					img.SetRGBA(x+cx*48, y+cy*48, color.RGBA{0, 0, 0, 0})
				}
			}
		}
	}
	corner(0, 0)
	corner(1, 0)
	corner(0, 1)
	corner(1, 1)
	// three share nodes + links
	node := func(x, y, r int) {
		for j := -r; j <= r; j++ {
			for i := -r; i <= r; i++ {
				if i*i+j*j <= r*r {
					img.Set(x+i, y+j, white)
				}
			}
		}
	}
	line := func(x0, y0, x1, y1 int) {
		steps := 40
		for s := 0; s <= steps; s++ {
			x := x0 + (x1-x0)*s/steps
			y := y0 + (y1-y0)*s/steps
			img.Set(x, y, white)
			img.Set(x+1, y, white)
		}
	}
	line(24, 32, 40, 22)
	line(24, 32, 40, 44)
	node(20, 32, 8)
	node(44, 20, 8)
	node(44, 44, 8)
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}
