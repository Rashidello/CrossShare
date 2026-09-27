# CrossShare

Copy on one computer. Paste on another. It really is that simple.

CrossShare keeps your clipboard and your files in sync across your devices. Copy text on your PC, paste it on your laptop. Drop a file in a folder, it shows up on your other machines. Screenshots and Explorer copies travel too, and they land ready to paste with Ctrl+V. If a device is offline, everything waits on the server until it comes back.

> **Status:** the public server is offline right now while we rebuild it for release. The Android app is headed to F-Droid. Check the Releases page for downloads and the current server address.

## Get going

Grab the agent for your system from Releases (Windows, Mac, and Linux builds are all there), then:

```bash
agent --server PASTE-SERVER-ADDRESS-HERE
```

First run asks for your email and password, then it just syncs in the background. Copy something and watch it appear on your other devices.

The day to day commands:

```bash
agent status             # are we connected, what's where
agent send "hello"       # push text right now
agent send-file ./a.zip  # push a file right now
agent devices            # every device on your account
agent disconnect         # pick one to kick off, numbered menu
agent folders            # where shared files live, and how to move them
agent set-ttl 7d         # how long stuff waits for offline devices
agent tray               # park it in the system tray, no console window
agent help               # the full reference with examples
```

Files work two ways. Either drop them in the send folder (see `agent folders` for the path) and they land in `received` everywhere else, or just copy them in Explorer like normal. Point the send folder at your Desktop with `agent set-send <path> --keep` and everything in it uploads once, stays put, and re-sends when you edit it.

On Android, install the APK (F-Droid soon), log in with the same account, and allow notifications. Copy something on your phone, tap Send clipboard in the notification, and it lands on your PC. Stuff from your PC pastes itself onto the phone automatically.

## What's inside

| Folder | What it is |
|--------|-----------|
| `server/` | The relay. Accounts, live routing, file storage, offline queue. |
| `agent/` | The desktop program. Clipboard watch, file sync, terminal commands, tray icon, and a point and click GUI in `agent/gui`. |
| `android/` | The native Android app, written in Kotlin. |
| `web/` | A browser version with the same features, served by the relay itself. |

Big files upload in the background and resume where they were going. Anything compressible goes over the wire gzipped. Anything you set to wait longer than 30 days gets capped there, anything under a minute gets bumped up to it.

## License

AGPLv3, see `LICENSE`. Contributions welcome.
