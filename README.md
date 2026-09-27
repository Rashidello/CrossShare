# CrossShare

Copy on one device. Paste on another — text, files, screenshots. No LAN pairing, no cables, no cloud drive in between.

CrossShare is an account-based relay system: your devices hold a persistent WebSocket to a small Go server, which forwards clipboard and file pushes between them in ~100ms. Offline devices collect what's waiting when they come back.

> **Status:** the public relay server is currently **offline** — it's being reworked and will be **released soon**. The Android app is planned for release on **F-Droid**. Until then, you can self-host the server in one command (see below).

## How it works

1. Run the relay server anywhere (your PC, a VPS, Railway — it's one binary).
2. Run the agent on each computer, log in once with email + password.
3. Copy anything. It appears on your other devices. Drop files in the send folder and they land on the others.

Text, Explorer-copied files, folders (sent as `.zip`) and screenshots all sync PC ↔ PC and arrive pastable with `Ctrl+V`. Phones send/receive text and files through the app. Offline items wait on the server (configurable, 1 minute – 30 days).

## Quick start (CLI)

```bash
# 1. Start the server (one window)
cd server
go run .                 # listens on :8080

# 2. Expose it (pick one)
cloudflared tunnel --url http://localhost:8080   # free, URL changes on restart
# ...or deploy server/ to Railway for a permanent URL (Dockerfile included)

# 3. Start the agent (another window, on each computer)
agent --server https://YOUR-SERVER-URL
# first run asks email + password, then syncs. That's it.

agent status             # connection, devices, inbox, folder paths
agent send "hello"       # push text to your other devices now
agent send-file ./a.zip  # push a file now
agent devices            # list every device on your account
agent disconnect         # pick a device to disconnect (numbered menu)
agent folders            # show / change shared folders
agent set-ttl 7d         # how long items wait for offline devices
agent tray               # hide in the system tray instead of a console
agent help               # full reference with examples
```

Files without the CLI: drop them into the send folder (`agent folders` shows where) — received files land in the receive folder. Point the send folder at anywhere (even Desktop) with `agent set-send <path> --keep`: everything uploads once, files stay put, edits resend.

## Components

| Path | Language | What |
|------|----------|------|
| `server/` | Go | Relay: accounts, WebSocket hub, SQLite store-and-forward, blob storage, serves the web UI |
| `agent/` | Go | Desktop agent: clipboard watch, file watcher, local API `:9876`, CLI, tray, GUI (`agent/gui`) |
| `android/` | Kotlin | Native Android app: background sync, notification / Quick-Settings-tile / inline-reply sending |
| `web/` | HTML+JS | Browser fallback with the same features, served by the server |
| `plugin/` | — | Reserved for the IntelliJ plugin |

Builds: `go build` in `server/` or `agent/` (Windows builds need mingw, see repo notes), Android via Android Studio, APKs ship through GitHub Releases and (soon) F-Droid.

## Protocol (short version)

- `POST /api/login` with email + password + device info → device token.
- WebSocket `/ws`: `hello` to auth, `push` to send (text/file/image, inline or `blob_id`, optional gzip), `deliver` to receive, `ack` to confirm, `presence` for the device list, `revoke` to disconnect.
- Big files ride `POST /api/blobs` (raw bytes, bearer token).

## License

AGPLv3 — see `LICENSE`.
