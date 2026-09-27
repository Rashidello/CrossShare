# CrossShare Android App

Native Kotlin client for CrossShare: background clipboard sync with your PCs.

## How it works (and why it works this way)

- **Receiving (PC → phone)** happens fully in the background: writing to the
  clipboard is allowed from background on all Android versions. Incoming text
  is pasted to your clipboard automatically + you get a notification.
- **Sending (phone → PC)** cannot happen silently in the background:
  Android 10+ blocks clipboard *reads* unless the app is in the foreground
  (or is the keyboard). So the app keeps a persistent notification:
  copy something → notification says "Copy detected — send to PC?" →
  tap **Send clipboard** → a transparent foreground activity reads the
  clipboard and pushes it over the WebSocket. One tap, no app to open.

## Build

1. Install [Android Studio](https://developer.android.com/studio) (Koala or newer, needs JDK 17).
2. Open the `android/` folder (not the repo root).
3. Let it sync Gradle, then **Run ▶** on a phone (or Build → Build APK).
4. On the phone: log in with the same account as your PC agent, allow
   notifications when asked.

The server URL field is prefilled with the current test tunnel URL — it
changes every time `cloudflared` restarts, so update it if login fails with
"Network error".

## Files

| File | Role |
|------|------|
| `DevDropService.kt` | Foreground service: WebSocket, reconnect backoff, clipboard write on receive, persistent notification |
| `SendClipboardActivity.kt` | Transparent foreground activity: the only place the clipboard is *read* |
| `MainActivity.kt` | Login/register + send box + history (tap to copy) + logout |
| `Proto.kt` | Wire format, mirrors `agent/types.go` + `agent/clipboard.go` |
| `Store.kt` | Credentials + last-50 history in SharedPreferences |
| `BootReceiver.kt` | Resume sync after reboot |

## Protocol parity with the Go agent

- `POST /api/login` → `{device_id, token}`, `device_os: "android"`
- WS hello: `{type:hello, device_id, token, name, os}`
- Push: `{type:push, item_id, kind:text, mime:text/plain, size, sha256, ttl_s:1800, targets:"all", payload:b64}`
- Deliver → apply to clipboard (live only; backlog goes to history) → ack `{type:ack, item_id, state:"received"}`
- Echo suppression via sha256 (`lastSentHash`/`lastAppliedHash`), same as the desktop agent.

## Permissions used (and why)

- `INTERNET` — WebSocket + login.
- `POST_NOTIFICATIONS` — status + "copy detected" + "received" alerts.
- `FOREGROUND_SERVICE` + `FOREGROUND_SERVICE_DATA_SYNC` — keep the sync socket alive.
- `RECEIVE_BOOT_COMPLETED` — resume after reboot.
