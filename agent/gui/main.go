package main

// CrossShare desktop GUI (Fyne). Talks to the RUNNING agent through its
// local API (127.0.0.1:9876) — start tray.exe or agent first.
// Build: go build -ldflags -H=windowsgui -o gui.exe ./gui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type statusResp struct {
	Connected  bool   `json:"connected"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	ServerURL  string `json:"server_url"`
}

type deviceResp struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	OS       string `json:"os"`
	Online   bool   `json:"online"`
	LastSeen string `json:"last_seen"`
}

type inboxResp struct {
	ItemID           string `json:"item_id"`
	Kind             string `json:"kind"`
	Mime             string `json:"mime"`
	Filename         string `json:"filename"`
	Size             int64  `json:"size"`
	OriginDeviceName string `json:"origin_device_name"`
	CreatedAt        string `json:"created_at"`
	Payload          string `json:"payload"`
	FilePath         string `json:"file_path"`
}

type guiClient struct {
	base  string
	token string
	http  *http.Client
}

// compactTheme wraps the dark theme with tighter padding/spacing so the
// GUI looks like a dense dev tool.
type compactTheme struct {
	fyne.Theme
}

func (t compactTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 2
	case theme.SizeNameInnerPadding:
		return 4
	case theme.SizeNameLineSpacing:
		return 2
	}
	return t.Theme.Size(name)
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devdrop", "config.json")
}

func readConfigMap() map[string]interface{} {
	m := map[string]interface{}{}
	b, err := os.ReadFile(configPath())
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	if m == nil {
		return map[string]interface{}{}
	}
	return m
}

func configString(key string) string {
	if v, ok := readConfigMap()[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func configInt(key string, def int) int {
	v, ok := readConfigMap()[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return def
}

// writeConfigKey updates a single key in ~/.devdrop/config.json without
// dropping other keys.
func writeConfigKey(key string, value interface{}) error {
	path := configPath()
	m := readConfigMap()
	m[key] = value
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func loadLocalClient() *guiClient {
	home, _ := os.UserHomeDir()
	port := 9876
	token := ""
	if b, err := os.ReadFile(filepath.Join(home, ".devdrop", "config.json")); err == nil {
		var cfg struct {
			LocalAPIPort  int    `json:"local_api_port"`
			LocalAPIToken string `json:"local_api_token"`
			SendDir       string `json:"send_dir"`
			ReceivedDir   string `json:"received_dir"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			if cfg.LocalAPIPort != 0 {
				port = cfg.LocalAPIPort
			}
			token = cfg.LocalAPIToken
		}
	}
	return &guiClient{
		base:  fmt.Sprintf("http://127.0.0.1:%d", port),
		token: token,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *guiClient) get(path string, out interface{}) error {
	req, _ := http.NewRequest("GET", c.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *guiClient) post(path string, body interface{}) error {
	var buf bytes.Buffer
	json.NewEncoder(&buf).Encode(body)
	req, _ := http.NewRequest("POST", c.base+path, &buf)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func openPath(dir string) {
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
	cmd.Start()
}

func humanSize(n int64) string {
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

func main() {
	a := app.NewWithID("com.crossshare.gui")
	// Force dark regardless of OS setting.
	a.Settings().SetTheme(theme.DarkTheme())
	// Tighten padding/spacing on top of dark so rows stay compact.
	a.Settings().SetTheme(compactTheme{Theme: theme.DarkTheme()})
	w := a.NewWindow("CrossShare")
	w.Resize(fyne.NewSize(720, 560))
	client := loadLocalClient()

	// This device id, learned from /api/status; used to pin Devices sort.
	thisDeviceID := ""

	statusLabel := widget.NewLabel("Checking agent…")
	statusLabel.TextStyle = fyne.TextStyle{Monospace: true}

	// Read-only settings labels, refreshed from live status (fallback: config).
	serverURLLabel := widget.NewLabel(configString("server_url"))
	serverURLLabel.TextStyle = fyne.TextStyle{Monospace: true}
	serverURLLabel.Wrapping = fyne.TextTruncate
	deviceNameLabel := widget.NewLabel(configString("device_name"))
	deviceNameLabel.TextStyle = fyne.TextStyle{Monospace: true}
	deviceNameLabel.Wrapping = fyne.TextTruncate

	refreshStatus := func() {
		var st statusResp
		if err := client.get("/api/status", &st); err != nil {
			fyne.Do(func() {
				statusLabel.SetText("Agent not running — start tray.exe or agent first.")
			})
			return
		}
		thisDeviceID = st.DeviceID
		state := "offline"
		if st.Connected {
			state = "connected"
		}
		fyne.Do(func() {
			statusLabel.SetText(fmt.Sprintf("%s • %s • %s", st.DeviceName, state, st.ServerURL))
			serverURLLabel.SetText(st.ServerURL)
			deviceNameLabel.SetText(st.DeviceName)
		})
	}

	// ---- Send tab ----
	sendEntry := widget.NewMultiLineEntry()
	sendEntry.SetPlaceHolder("Type text to send to your other devices…")
	sendEntry.Wrapping = fyne.TextWrapWord
	sendBtn := widget.NewButton("Send to devices", func() {
		text := sendEntry.Text
		if text == "" {
			return
		}
		go func() {
			if err := client.post("/api/send", map[string]string{"kind": "text", "content": text, "targets": "all"}); err != nil {
				fyne.Do(func() { dialog.ShowError(err, w) })
				return
			}
			fyne.Do(func() { sendEntry.SetText("") })
		}()
	})
	sendFileBtn := widget.NewButton("Send a file…", func() {
		dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil || reader == nil {
				return
			}
			path := reader.URI().Path()
			reader.Close()
			go func() {
				if err := client.post("/api/send", map[string]string{"kind": "file", "file_path": path, "targets": "all"}); err != nil {
					fyne.Do(func() { dialog.ShowError(err, w) })
				}
			}()
		}, w)
	})
	sendTab := container.NewBorder(nil, container.NewVBox(sendBtn, sendFileBtn), nil, nil, sendEntry)

	// ---- Inbox tab ----
	var inboxItems []inboxResp
	selectedInbox := -1
	inboxList := widget.NewList(
		func() int { return len(inboxItems) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			it := inboxItems[id]
			title := it.Filename
			if title == "" {
				title = it.Kind
			}
			if len(title) > 60 {
				title = title[:57] + "…"
			}
			obj.(*widget.Label).SetText(fmt.Sprintf("%s · %s · %s", it.Kind, title, it.OriginDeviceName))
		},
	)
	inboxDetail := widget.NewLabel("Select an item to preview it here.")
	inboxList.OnSelected = func(id widget.ListItemID) {
		selectedInbox = int(id)
		if id < 0 || int(id) >= len(inboxItems) {
			return
		}
		it := inboxItems[id]
		preview := it.Filename
		if it.Kind == "text" && it.Payload != "" {
			preview = it.Payload
			if len(preview) > 300 {
				preview = preview[:300] + "…"
			}
		} else if it.Kind == "file" {
			preview = fmt.Sprintf("%s (%s)\nSaved at: %s", it.Filename, humanSize(it.Size), it.FilePath)
		}
		inboxDetail.SetText(preview)
	}
	copyBtn := widget.NewButton("Copy text", func() {
		sel := selectedInbox
		if sel < 0 || sel >= len(inboxItems) {
			return
		}
		it := inboxItems[sel]
		if it.Kind != "text" || it.Payload == "" {
			return
		}
		w.Clipboard().SetContent(it.Payload)
	})
	refreshInbox := func() {
		var items []inboxResp
		if err := client.get("/api/inbox", &items); err != nil {
			return
		}
		// newest first
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		fyne.Do(func() {
			inboxItems = items
			inboxList.Refresh()
		})
	}
	inboxTab := container.NewBorder(nil, container.NewHBox(copyBtn), nil, nil,
		container.NewVSplit(container.NewScroll(inboxList), container.NewScroll(inboxDetail)))

	// ---- Devices tab ----
	var devItems []deviceResp
	selectedDev := -1
	devList := widget.NewList(
		func() int { return len(devItems) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			d := devItems[id]
			dot := "○"
			if d.Online {
				dot = "●"
			}
			label := fmt.Sprintf("%s %s (%s)", dot, d.Name, d.OS)
			if d.ID != "" && d.ID == thisDeviceID {
				label += " ★ This PC"
			}
			obj.(*widget.Label).SetText(label)
		},
	)
	devList.OnSelected = func(id widget.ListItemID) {
		selectedDev = int(id)
	}
	refreshDevices := func() {
		var devs []deviceResp
		if err := client.get("/api/devices", &devs); err != nil {
			return
		}
		self := thisDeviceID
		sort.Slice(devs, func(i, j int) bool {
			iSelf := devs[i].ID != "" && devs[i].ID == self
			jSelf := devs[j].ID != "" && devs[j].ID == self
			if iSelf != jSelf {
				return iSelf
			}
			if devs[i].Name != devs[j].Name {
				return devs[i].Name < devs[j].Name
			}
			return devs[i].ID < devs[j].ID
		})
		fyne.Do(func() {
			devItems = devs
			if selectedDev >= len(devItems) {
				selectedDev = -1
			}
			devList.Refresh()
		})
	}
	disconnectBtn := widget.NewButton("Disconnect selected", func() {
		sel := selectedDev
		if sel < 0 || sel >= len(devItems) {
			return
		}
		d := devItems[sel]
		dialog.ShowConfirm("Disconnect device",
			fmt.Sprintf("Disconnect '%s' (%s)?", d.Name, d.OS),
			func(ok bool) {
				if !ok {
					return
				}
				go func() {
					if err := client.post("/api/revoke", map[string]string{"device_id": d.ID}); err != nil {
						fyne.Do(func() { dialog.ShowError(err, w) })
						return
					}
					refreshDevices()
				}()
			}, w)
	})
	disconnectOthersBtn := widget.NewButton("Disconnect all others", func() {
		others := []deviceResp{}
		for _, d := range devItems {
			if d.ID == "" || d.ID == thisDeviceID {
				continue
			}
			others = append(others, d)
		}
		if len(others) == 0 {
			dialog.ShowInformation("Nothing to do", "No other devices to disconnect.", w)
			return
		}
		dialog.ShowConfirm("Disconnect all others",
			fmt.Sprintf("Disconnect %d other device(s)? This PC stays connected.", len(others)),
			func(ok bool) {
				if !ok {
					return
				}
				go func() {
					var firstErr error
					for _, d := range others {
						if err := client.post("/api/revoke", map[string]string{"device_id": d.ID}); err != nil && firstErr == nil {
							firstErr = err
						}
					}
					if firstErr != nil {
						fyne.Do(func() { dialog.ShowError(firstErr, w) })
					}
					refreshDevices()
				}()
			}, w)
	})
	devTab := container.NewBorder(nil, container.NewVBox(disconnectBtn, disconnectOthersBtn), nil, nil, container.NewScroll(devList))

	// ---- Folders tab ----
	home, _ := os.UserHomeDir()
	var dirCfg struct {
		SendDir     string `json:"send_dir"`
		ReceivedDir string `json:"received_dir"`
	}
	if b, err := os.ReadFile(filepath.Join(home, ".devdrop", "config.json")); err == nil {
		json.Unmarshal(b, &dirCfg)
	}
	foldersTab := container.NewVBox(
		widget.NewLabel("Drop files into Send — they upload to your other devices."),
		widget.NewButton("Open send folder: "+dirCfg.SendDir, func() { openPath(dirCfg.SendDir) }),
		widget.NewButton("Open received folder: "+dirCfg.ReceivedDir, func() { openPath(dirCfg.ReceivedDir) }),
		widget.NewLabel("Change folders anytime with: agent set-send / agent set-recv"),
	)

	// ---- Settings tab ----
	sendDirLabel := widget.NewLabel(configString("send_dir"))
	sendDirLabel.Wrapping = fyne.TextTruncate
	changeSendBtn := widget.NewButton("Change…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			dir := uri.Path()
			if dir == "" {
				return
			}
			if err := writeConfigKey("send_dir", dir); err != nil {
				dialog.ShowError(err, w)
				return
			}
			sendDirLabel.SetText(dir)
		}, w)
	})
	recvDirLabel := widget.NewLabel(configString("received_dir"))
	recvDirLabel.Wrapping = fyne.TextTruncate
	changeRecvBtn := widget.NewButton("Change…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}
			dir := uri.Path()
			if dir == "" {
				return
			}
			if err := writeConfigKey("received_dir", dir); err != nil {
				dialog.ShowError(err, w)
				return
			}
			recvDirLabel.SetText(dir)
		}, w)
	})

	ttlLabels := []string{"1 minute", "30 minutes", "1 hour", "1 day", "7 days", "30 days"}
	ttlValues := []int{60, 1800, 3600, 86400, 604800, 2592000}
	ttlToLabel := map[int]string{}
	ttlToValue := map[string]int{}
	for i, l := range ttlLabels {
		ttlToLabel[ttlValues[i]] = l
		ttlToValue[l] = ttlValues[i]
	}
	currentTTL := configInt("default_ttl_s", 1800)
	if currentTTL == 0 {
		currentTTL = 1800
	}
	ttlSelect := widget.NewSelect(ttlLabels, func(s string) {
		v, ok := ttlToValue[s]
		if !ok {
			return
		}
		if err := writeConfigKey("default_ttl_s", v); err != nil {
			fyne.Do(func() { dialog.ShowError(err, w) })
		}
	})
	if l, ok := ttlToLabel[currentTTL]; ok {
		ttlSelect.SetSelected(l)
	} else {
		ttlSelect.SetSelected("30 minutes")
	}

	settingsTab := container.NewVBox(
		widget.NewLabelWithStyle("Settings", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewSeparator(),
		widget.NewLabel("Files you drop here get sent to your other devices:"),
		container.NewBorder(nil, nil, nil, changeSendBtn, sendDirLabel),
		widget.NewLabel("New files from your other devices land here:"),
		container.NewBorder(nil, nil, nil, changeRecvBtn, recvDirLabel),
		widget.NewLabel("How long files wait on the server for offline devices:"),
		ttlSelect,
		widget.NewLabel("Server address (change with: agent --server):"),
		serverURLLabel,
		widget.NewLabel("This device's name:"),
		deviceNameLabel,
		widget.NewLabel("Folder and keep changes need an agent/tray restart to take effect."),
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Send", sendTab),
		container.NewTabItem("Inbox", inboxTab),
		container.NewTabItem("Devices", devTab),
		container.NewTabItem("Folders", foldersTab),
		container.NewTabItem("Settings", container.NewScroll(settingsTab)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	top := container.NewVBox(statusLabel, widget.NewSeparator())
	w.SetContent(container.NewBorder(top, nil, nil, nil, tabs))

	refreshStatus()
	refreshInbox()
	refreshDevices()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			refreshStatus()
			refreshInbox()
			refreshDevices()
		}
	}()

	w.ShowAndRun()
}
