package main

// Friendly one-shot commands for the CrossShare desktop agent.
// Usage: agent <command> [args] [--config path]
// Commands talk to the RUNNING agent through its local API (127.0.0.1:9876),
// so the agent must already be running for everything except `help`.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func printHelp() {
	fmt.Println(`CrossShare — copy on one device, paste on another.

USAGE:
  agent                          Start the agent (runs in this window)
  agent --server <url>            Start and connect to a different server
  agent status                   Show connection, device and inbox summary
  agent devices                  List every device on your account
  agent send "some text"          Send clipboard text to your other devices
  agent send-file <path>          Send a file to your other devices
  agent disconnect <device-id>    Disconnect (revoke) a device by its ID
  agent tray                      Minimize to the system tray (no console window*)
  agent folders                   Show the shared send/receive folders
  agent set-send <path> [--keep|--move]  Choose folder (keep is default:
                                    files stay, edits resend)
  agent set-recv <path>           Choose where received files land
  agent set-ttl <duration>        How long items wait on server (e.g. 10m, 2h, 7d)
  agent logout                   Log this PC out (keeps the server URL)
  agent logout-others            Disconnect every other device on your account
  agent install                  Make 'agent' work from any folder (adds to PATH)
  agent login                    Log in again (fresh device on this PC)
  agent help                     Show this help

HOW IT WORKS:
  - The agent watches your clipboard. Copy anything and it appears
    on your other devices a moment later. No buttons needed on PC.
    On PC, copied files, folders (sent as .zip) and screenshots sync
    too, and arrive pastable with Ctrl+V on your other PCs.
  - Drop files into the "send" folder (see 'agent status' for the path)
    and they land in "received" on your other devices.
  - Hate the extra console window? Run 'agent tray' (or tray.exe):
    CrossShare lives in the taskbar tray instead, and restarts
    the sync automatically if it ever stops.
  - Your phone: open the server URL in a browser (or the CrossShare app),
    log in with the same email and password.

EXAMPLES:
  agent --server https://abc123.trycloudflare.com
  agent send "meeting notes: ..."
  agent send-file C:\Users\me\Desktop\report.pdf
  agent set-recv C:\Users\me\Documents\Shared
  agent disconnect 51f5774d-d5e7-49a7-92cf-70c0713a21b1`)
}

type cliClient struct {
	base  string
	token string
	http  *http.Client
}

func newCLIClient(cfg *Config) *cliClient {
	return &cliClient{
		base:  fmt.Sprintf("http://127.0.0.1:%d", cfg.LocalAPIPort),
		token: cfg.LocalAPIToken,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *cliClient) get(path string, out interface{}) error {
	req, _ := http.NewRequest("GET", c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *cliClient) post(path string, body interface{}, out interface{}) error {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest("POST", c.base+path, &buf)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// runCLICommand returns (handled, exitCode).
func runCLICommand(args []string, cfg *Config, configPath string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "help", "--help", "-h":
		printHelp()
		return true, 0
	case "status":
		return true, cliStatus(cfg)
	case "devices":
		return true, cliDevices(cfg)
	case "send":
		if len(rest) == 0 {
			if !isTerminal() {
				fmt.Println("Usage: agent send \"some text\"")
				return true, 1
			}
			text := promptLine("Text to send (empty cancels): ")
			if text == "" {
				fmt.Println("Cancelled.")
				return true, 0
			}
			return true, cliSend(cfg, text)
		}
		return true, cliSend(cfg, strings.Join(rest, " "))
	case "send-file":
		if len(rest) == 0 {
			if !isTerminal() {
				fmt.Println("Usage: agent send-file <path-to-file>")
				return true, 1
			}
			path := promptLine("File to send (empty cancels): ")
			if path == "" {
				fmt.Println("Cancelled.")
				return true, 0
			}
			return true, cliSendFile(cfg, path)
		}
		return true, cliSendFile(cfg, rest[0])
	case "disconnect":
		if len(rest) == 0 {
			if !isTerminal() {
				fmt.Println("Usage: agent disconnect <device-id>")
				fmt.Println("Tip: run 'agent devices' to see IDs.")
				return true, 1
			}
			return true, cliDisconnectPick(cfg)
		}
		return true, cliDisconnect(cfg, rest[0])
	case "folders":
		return true, cliFolders(cfg)
	case "set-send":
		args, keepChanged, keep := splitKeepFlag(rest)
		if len(args) == 0 {
			if !isTerminal() {
				fmt.Println("Usage: agent set-send <folder-path> [--keep | --move]")
				return true, 1
			}
			fmt.Printf("Current send folder: %s\n", cfg.SendDir)
			path := promptLine("New send folder (empty keeps current): ")
			if path == "" && !keepChanged {
				fmt.Println("Kept as is.")
				return true, 0
			}
			if path == "" {
				path = cfg.SendDir
			}
			return true, cliSetDir(cfg, configPath, "send", path, keepChanged, keep)
		}
		if keepChanged && keep {
			warnBulkUpload(args[0])
		}
		return true, cliSetDir(cfg, configPath, "send", args[0], keepChanged, keep)
	case "set-recv", "set-receive":
		if len(rest) == 0 {
			if !isTerminal() {
				fmt.Println("Usage: agent set-recv <folder-path>")
				return true, 1
			}
			fmt.Printf("Current receive folder: %s\n", cfg.ReceivedDir)
			path := promptLine("New receive folder (empty keeps current): ")
			if path == "" {
				fmt.Println("Kept as is.")
				return true, 0
			}
			return true, cliSetDir(cfg, configPath, "receive", path, false, false)
		}
		return true, cliSetDir(cfg, configPath, "receive", rest[0], false, false)
	case "set-ttl":
		if len(rest) == 0 {
			fmt.Printf("Items currently wait on the server for %s.\n", formatTTL(int64(cfg.TTL())))
			if !isTerminal() {
				fmt.Println("Usage: agent set-ttl <duration>   (e.g. 10m, 2h, 7d; min 1m, max 30d)")
				return true, 1
			}
			ans := promptLine("How long should items wait for offline devices? (empty keeps current): ")
			if ans == "" {
				fmt.Println("Kept as is.")
				return true, 0
			}
			return true, cliSetTTL(cfg, configPath, ans)
		}
		return true, cliSetTTL(cfg, configPath, rest[0])
	case "logout":
		return true, cliLogout(cfg, configPath)
	case "logout-others":
		return true, cliLogoutOthers(cfg)
	case "install":
		return true, cliInstall()
	case "login":
		return true, cliLogin(cfg, configPath)
	case "tray":
		runTray()
		return true, 0
	}
	return false, 0
}

func needAgent(err error) int {
	fmt.Println("Could not reach the running agent on 127.0.0.1.")
	fmt.Println("Start it first in another window:  agent")
	if err != nil {
		fmt.Printf("Details: %v\n", err)
	}
	return 1
}

func cliStatus(cfg *Config) int {
	c := newCLIClient(cfg)
	var st struct {
		Connected  bool   `json:"connected"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		ServerURL  string `json:"server_url"`
	}
	if err := c.get("/api/status", &st); err != nil {
		return needAgent(err)
	}
	var devices []Device
	_ = c.get("/api/devices", &devices)
	var inbox []InboxItem
	_ = c.get("/api/inbox", &inbox)

	online := 0
	for _, d := range devices {
		if d.Online {
			online++
		}
	}
	conn := "offline"
	if st.Connected {
		conn = "connected"
	}
	fmt.Printf("Device:   %s\n", st.DeviceName)
	fmt.Printf("Server:   %s (%s)\n", st.ServerURL, conn)
	fmt.Printf("Devices:  %d online of %d total  (see 'agent devices')\n", online, len(devices))
	fmt.Printf("Inbox:    %d received item(s)\n", len(inbox))
	fmt.Printf("Keeps:    items wait %s on the server  (change: agent set-ttl 2h)\n", formatTTL(int64(cfg.TTL())))
	fmt.Printf("Folders:  drop files in %s  ->  they arrive in %s\n", cfg.SendDir, cfg.ReceivedDir)
	return 0
}

func cliDevices(cfg *Config) int {
	c := newCLIClient(cfg)
	var devices []Device
	if err := c.get("/api/devices", &devices); err != nil {
		return needAgent(err)
	}
	if len(devices) == 0 {
		fmt.Println("No devices on this account yet.")
		return 0
	}
	fmt.Printf("%-10s %-24s %-10s %-8s %s\n", "STATUS", "NAME", "OS", "YOU", "DEVICE ID (for 'agent disconnect')")
	for _, d := range devices {
		status := "offline"
		if d.Online {
			status = "online"
		}
		you := ""
		if d.ID == cfg.DeviceID {
			you = "<- this PC"
		}
		name := d.Name
		if len(name) > 24 {
			name = name[:23] + "…"
		}
		fmt.Printf("%-10s %-24s %-10s %-8s %s\n", status, name, d.OS, you, d.ID)
	}
	return 0
}

func cliSend(cfg *Config, text string) int {
	c := newCLIClient(cfg)
	var res map[string]string
	err := c.post("/api/send", map[string]string{
		"kind":    "text",
		"content": text,
		"targets": "all",
	}, &res)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return needAgent(err)
		}
		fmt.Printf("Send failed: %v\n", err)
		return 1
	}
	fmt.Println("Sent to your other devices.")
	return 0
}

func cliSendFile(cfg *Config, path string) int {
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Printf("Cannot read file: %v\n", err)
		return 1
	}
	if fi.IsDir() {
		fmt.Println("That is a folder — point at a single file.")
		return 1
	}
	c := newCLIClient(cfg)
	var res map[string]string
	err = c.post("/api/send", map[string]string{
		"kind":      "file",
		"file_path": path,
		"targets":   "all",
	}, &res)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return needAgent(err)
		}
		fmt.Printf("Send failed: %v\n", err)
		return 1
	}
	fmt.Printf("Sent %s to your other devices.\n", fi.Name())
	return 0
}

func cliDisconnect(cfg *Config, deviceID string) int {
	c := newCLIClient(cfg)
	var devices []Device
	if err := c.get("/api/devices", &devices); err != nil {
		return needAgent(err)
	}
	var target *Device
	for i, d := range devices {
		if d.ID == deviceID || strings.HasPrefix(d.ID, deviceID) {
			target = &devices[i]
			break
		}
	}
	if target == nil {
		fmt.Println("No device with that ID. Run 'agent devices' to see IDs.")
		return 1
	}
	selfWarn := ""
	if target.ID == cfg.DeviceID {
		selfWarn = "  WARNING: this is THIS PC — you will log yourself out."
	}
	fmt.Printf("Disconnect '%s' (%s)?%s  [y/N]: ", target.Name, target.OS, selfWarn)
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("Cancelled.")
		return 0
	}
	var res map[string]string
	if err := c.post("/api/revoke", map[string]string{"device_id": target.ID}, &res); err != nil {
		fmt.Printf("Disconnect failed: %v\n", err)
		return 1
	}
	fmt.Printf("Disconnected '%s'.\n", target.Name)
	return 0
}

func cliFolders(cfg *Config) int {
	mode := "files move to .sent after sharing"
	if cfg.SendKeep {
		mode = "files STAY in place, resend when changed"
	}
	fmt.Println("Shared folders on this PC:")
	fmt.Printf("  Send (drop files here):  %s\n", cfg.SendDir)
	fmt.Printf("    mode: %s\n", mode)
	fmt.Printf("  Receive (files land):    %s\n", cfg.ReceivedDir)
	fmt.Println()
	fmt.Println("Change them with:")
	fmt.Println("  agent set-send <folder> [--keep | --move]")
	fmt.Println("    --keep: watch a folder like Desktop — everything in it uploads,")
	fmt.Println("            files stay, edits resend. Everything already inside")
	fmt.Println("            uploads once on next start (over-100MB files skipped).")
	fmt.Println("    --move: (default) files move to .sent after sharing.")
	fmt.Println("  agent set-recv <folder-path>   (where received files land)")
	fmt.Println("Then restart the agent so it watches the new folder.")
	return 0
}

func cliSetDir(cfg *Config, configPath, which, rawPath string, keepChanged, keep bool) int {
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		fmt.Printf("Bad path: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		fmt.Printf("Cannot use that folder: %v\n", err)
		return 1
	}
	label := "Send"
	if which == "receive" {
		label = "Receive"
		cfg.ReceivedDir = abs
	} else {
		cfg.SendDir = abs
		if keepChanged {
			cfg.SendKeep = keep
		}
	}
	if err := saveConfig(cfg, configPath); err != nil {
		fmt.Printf("Could not save config: %v\n", err)
		return 1
	}
	fmt.Printf("%s folder set to:\n  %s\n", label, abs)
	if which == "send" {
		if cfg.SendKeep {
			fmt.Println("Mode: KEEP — everything inside uploads, files stay, edits resend.")
		} else {
			fmt.Println("Mode: MOVE — files move to .sent after sharing.")
		}
	}
	fmt.Println("Restart the agent so it picks the new folder up.")
	return 0
}

// splitKeepFlag pulls --keep/--move out of args.
func splitKeepFlag(args []string) (rest []string, changed bool, keep bool) {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "--keep":
			changed, keep = true, true
		case "--move":
			changed, keep = true, false
		default:
			rest = append(rest, a)
		}
	}
	return rest, changed, keep
}

// warnBulkUpload tells the user what's about to happen before they point
// --keep at a folder full of stuff (like Desktop).
func warnBulkUpload(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // doesn't exist yet — nothing to warn about
	}
	var n int64
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil {
			n++
			total += fi.Size()
		}
	}
	if n == 0 {
		return
	}
	fmt.Printf("NOTE: %d file(s) (%s) are already in there — all of them will\n", n, humanBytes(total))
	fmt.Printf("upload once on next start, then only new/changed files. Over-100MB\n")
	fmt.Printf("files are skipped.")
	if !isTerminal() {
		fmt.Printf("\n")
		return
	}
	fmt.Printf(" Press Enter to continue, Ctrl+C to abort: ")
	reader := bufio.NewReader(os.Stdin)
	reader.ReadString('\n')
}

// cliSetTTL sets how long items wait on the server for offline devices.
// Accepts Go durations ("90s", "10m", "2h", "7d") or plain seconds ("300").
// Clamped to 1 minute .. 30 days.
func cliSetTTL(cfg *Config, configPath, raw string) int {
	secs, err := parseTTL(raw)
	if err != nil {
		fmt.Printf("I don't understand '%s'. Try 10m, 2h, 7d, or seconds like 600.\n", raw)
		return 1
	}
	cfg.DefaultTTLS = secs
	if err := saveConfig(cfg, configPath); err != nil {
		fmt.Printf("Could not save config: %v\n", err)
		return 1
	}
	fmt.Printf("Done — items now wait %s on the server for offline devices.\n", formatTTL(int64(secs)))
	fmt.Println("(Takes effect for new items immediately; no restart needed.)")
	return 0
}

func parseTTL(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty")
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return clampTTL(int(d.Seconds())), nil
	}
	var secs int
	if _, err := fmt.Sscanf(raw, "%d", &secs); err == nil {
		return clampTTL(secs), nil
	}
	lower := strings.ToLower(raw)
	mult := 0
	num := ""
	if strings.HasSuffix(lower, "d") {
		mult = 86400
		num = raw[:len(raw)-1]
	} else if strings.HasSuffix(lower, "w") {
		mult = 7 * 86400
		num = raw[:len(raw)-1]
	}
	if mult > 0 {
		var n int
		if _, err := fmt.Sscanf(num, "%d", &n); err == nil {
			return clampTTL(n * mult), nil
		}
	}
	return 0, fmt.Errorf("bad duration")
}

func clampTTL(secs int) int {
	if secs < 60 {
		return 60
	}
	if secs > 2592000 {
		return 2592000
	}
	return secs
}

func formatTTL(secs int64) string {
	if secs < 90 {
		return fmt.Sprintf("%d seconds", secs)
	}
	if secs < 5400 {
		m := secs / 60
		if m == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", m)
	}
	if secs < 129600 {
		h := secs / 3600
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	d := secs / 86400
	if d == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", d)
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1024*1024*1024))
}

// cliLogoutOthers revokes every device except this one, after confirmation.
func cliLogoutOthers(cfg *Config) int {
	c := newCLIClient(cfg)
	var devices []Device
	if err := c.get("/api/devices", &devices); err != nil {
		return needAgent(err)
	}
	var others []Device
	for _, d := range devices {
		if d.ID != cfg.DeviceID {
			others = append(others, d)
		}
	}
	if len(others) == 0 {
		fmt.Println("No other devices — only this PC is on the account.")
		return 0
	}
	fmt.Println("This will disconnect:")
	for _, d := range others {
		status := "offline"
		if d.Online {
			status = "online"
		}
		fmt.Printf("  - %s (%s, %s)\n", d.Name, d.OS, status)
	}
	fmt.Printf("This PC (%s) stays logged in. Continue? [y/N]: ", cfg.DeviceName)
	ans := promptLine("")
	if ans != "y" && ans != "yes" {
		fmt.Println("Cancelled.")
		return 0
	}
	var res map[string]string
	ok, fail := 0, 0
	for _, d := range others {
		if err := c.post("/api/revoke", map[string]string{"device_id": d.ID}, &res); err != nil {
			fmt.Printf("  ! %s: %v\n", d.Name, err)
			fail++
			continue
		}
		ok++
	}
	fmt.Printf("Disconnected %d device(s)%s.\n", ok, func() string {
		if fail > 0 {
			return fmt.Sprintf(" (%d failed)", fail)
		}
		return ""
	}())
	return 0
}

func cliLogin(cfg *Config, configPath string) int {
	if cfg.DeviceID != "" && isTerminal() {
		fmt.Printf("This PC is already logged in as %s.\n", displayAccount(cfg))
		ans := promptLine("Log in fresh anyway (creates a new device)? [y/N]: ")
		if ans != "y" && ans != "yes" {
			fmt.Println("Kept current login.")
			return 0
		}
	}
	cfg.DeviceID = ""
	cfg.DeviceToken = ""
	cfg.Email = ""
	saveConfig(cfg, configPath)
	doLoginFlow(cfg, configPath)
	return 0
}

func displayAccount(cfg *Config) string {
	if cfg.Email != "" {
		return cfg.Email
	}
	return cfg.DeviceName
}

func cliLogout(cfg *Config, configPath string) int {
	if cfg.DeviceID == "" {
		fmt.Println("This PC is not logged in.")
		return 0
	}
	fmt.Printf("Log out device '%s'? Syncing stops immediately.  [y/N]: ", cfg.DeviceName)
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("Cancelled.")
		return 0
	}
	cfg.DeviceID = ""
	cfg.DeviceToken = ""
	cfg.Email = ""
	if err := saveConfig(cfg, configPath); err != nil {
		fmt.Printf("Could not save config: %v\n", err)
		return 1
	}
	fmt.Println("Logged out. Stopping the running agent...")
	stopRunningAgents()
	fmt.Println("Done. Run 'agent' and log in again to reconnect.")
	return 0
}

// stopRunningAgents best-effort stops other agent.exe processes so logout
// (and account switches) take effect immediately. Never kills this process.
func stopRunningAgents() {
	self := os.Getpid()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq agent.exe", "/FO", "CSV", "/NH").Output()
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Split(line, ",")
			if len(fields) < 2 {
				continue
			}
			var pid int
			fmt.Sscanf(strings.Trim(fields[1], `"`), "%d", &pid)
			if pid != 0 && pid != self {
				exec.Command("taskkill", "/F", "/PID", fmt.Sprint(pid)).Run()
			}
		}
		return
	}
	// macOS/Linux: pkill would also match this CLI process, so only use it
	// when we are already done — caller prints everything before this.
	exec.Command("pkill", "-x", "agent").Run()
}

// readLineTimeout reads one stdin line, returning "" if nobody answers in
// time (headless/boot launches must never block).
// isTerminal reports whether stdin is an interactive console (not a pipe).
func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func promptLine(label string) string {
	fmt.Print(label)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

// cliDisconnectPick lists devices numbered and asks which to disconnect.
func cliDisconnectPick(cfg *Config) int {
	c := newCLIClient(cfg)
	var devices []Device
	if err := c.get("/api/devices", &devices); err != nil {
		return needAgent(err)
	}
	// Hide revoked/ourselves from the choices? Keep ourselves visible but warn.
	choices := devices
	if len(choices) == 0 {
		fmt.Println("No devices on this account yet.")
		return 0
	}
	fmt.Println("Which device to disconnect?")
	for i, d := range choices {
		status := "offline"
		if d.Online {
			status = "online"
		}
		you := ""
		if d.ID == cfg.DeviceID {
			you = "  <-- THIS PC (disconnecting logs you out)"
		}
		fmt.Printf("  %d) %s (%s, %s)%s\n", i+1, d.Name, d.OS, status, you)
	}
	ans := promptLine("Number (empty cancels): ")
	if ans == "" {
		fmt.Println("Cancelled.")
		return 0
	}
	var n int
	fmt.Sscanf(ans, "%d", &n)
	if n < 1 || n > len(choices) {
		fmt.Println("Not a valid number. Cancelled.")
		return 1
	}
	return cliDisconnect(cfg, choices[n-1].ID)
}

func readLineTimeout(d time.Duration) string {
	ch := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		ch <- line
	}()
	select {
	case line := <-ch:
		return line
	case <-time.After(d):
		fmt.Println()
		return ""
	}
}

func isSwitchAnswer(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "switch" || s == "s" || s == "change" || s == "logout"
}
