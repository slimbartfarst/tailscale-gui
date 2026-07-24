# tailscale-gui

A Linux system tray GUI for Tailscale, written entirely in Go.

**Built on:**
- [`tailscale.com/client/local`](https://pkg.go.dev/tailscale.com/client/local) — official Go client for the `tailscaled` daemon
- [`github.com/tailscale/systray`](https://github.com/tailscale/systray) — Tailscale's own cross-platform systray library (D-Bus / AppIndicator on Linux)

No networking code written here. The daemon does all the hard work.

---

## Features

| Feature | Status |
|---|---|
| Connect / Disconnect | ✅ |
| Live connection status in tray | ✅ |
| Per-peer list with online indicator | ✅ |
| Click peer IP to copy to clipboard | ✅ |
| Exit node selection submenu (with checkmarks) | ✅ |
| MagicDNS / Accept routes / Shields up toggles | ✅ |
| Desktop notifications on state change | ✅ |
| Taildrop file receive (auto-save to ~/Downloads/Taildrop) | ✅ |
| Taildrop receive — immediate via IPN bus (FilesWaiting signal) | ✅ |
| Taildrop receive — duplicate filename handling (file (1).txt etc.) | ✅ |
| Taildrop receive — sender hostname resolved from peer map | ✅ |
| Taildrop receive — action notification: "Open file" / "Show in folder" | ✅ |
| Taildrop receive — D-Bus action buttons via gdbus (with notify-send fallback) | ✅ |
| Taildrop send — multi-file picker (zenity) | ✅ |
| Taildrop send — peer picker (list dialog + text fallback + fuzzy match) | ✅ |
| Taildrop send — per-peer "Send file…" in tray submenu | ✅ |
| Taildrop send — progress bar (zenity --progress) | ✅ |
| Taildrop send — from status window browser UI | ✅ |
| Subnet route advertising — tray submenu with approval status | ✅ |
| Subnet route advertising — local interface suggestions | ✅ |
| Subnet route advertising — manual CIDR entry with validation | ✅ |
| Subnet route advertising — remove from tray | ✅ |
| Subnet route advertising — add/remove from status window | ✅ |
| SSH peer launch — "SSH…" sub-item per peer in tray (SSH-capable peers only) | ✅ |
| SSH peer launch — auto-detects terminal ($TERMINAL, DE preference, fallback list) | ✅ |
| SSH peer launch — "SSH…" button per peer in status window | ✅ |
| SSH peer launch — configurable terminal cmd and SSH user in config.json | ✅ |
| Per-peer ping from status window | ✅ |
| Autostart via XDG desktop entry | ✅ |

---

## What to build next

- **Multi-account / user switching** — watch for `ipn.NeedsLogin` and open
  the auth URL from `st.AuthURL` automatically in the browser.
- **Flatpak packaging** — add a `packaging/flatpak/` manifest for Flathub
  distribution with sandboxed access to the Tailscale socket.
- **System settings integration** — expose TerminalCmd and SSHUser in the
  status window so users can configure them without editing JSON.

---

## Prerequisites

| Requirement | Install |
|---|---|
| Go 1.23+ | https://go.dev/dl/ |
| `tailscaled` running | `sudo tailscaled &` or systemd |
| Operator permission | `sudo tailscale set --operator=$USER` |
| D-Bus + StatusNotifierItem | GNOME, KDE, COSMIC, waybar, hyprpanel |
| GNOME only | `gnome-shell-extension-appindicator` |
| Notifications (optional) | `sudo apt install libnotify-bin` |
| File send (optional) | `sudo apt install zenity` |
| Clipboard (optional) | `sudo apt install xclip` or `wl-clipboard` |

---

## Quick start

```bash
git clone https://github.com/yourname/tailscale-gui
cd tailscale-gui

# Downloads deps, generates placeholder icons, compiles
make

# Run (verbose)
make run
```

---

## Project layout

```
tailscale-gui/
├── cmd/tailscale-gui/main.go       Entry point
├── internal/
│   ├── client/client.go            Wraps tailscale.com/client/local
│   ├── systray/
│   │   ├── systray.go              Tray icon, menus, event loop
│   │   └── icons.go                Embedded PNGs + OS helpers (browser, clipboard, zenity)
│   ├── config/config.go            ~/.config/tailscale-gui/config.json
│   ├── notify/notify.go            Desktop notifications (notify-send)
│   ├── picker/
│   │   ├── picker.go               File picker, peer picker, progress dialog (zenity)
│   │   └── picker_test.go          Tests for matchPeer, osLabel, firstIP
│   ├── routes/
│   │   ├── routes.go               Subnet route advertising (read/write/suggest/validate)
│   │   └── routes_test.go          Tests for Validate, ParsePrefix, isPrivate, Suggest
│   ├── taildrop/taildrop.go        File send / receive
│   └── window/
│       ├── window.go               Localhost HTTP status server + API endpoints
│       └── html.go                 Self-contained dashboard HTML/CSS/JS
├── assets/icons/                   32×32 PNG tray icons (replace placeholders)
├── scripts/generate_icons.py       Generates placeholder icons
├── packaging/tailscale-gui.desktop XDG autostart entry
└── Makefile
```

---

## Architecture

```
  ┌────────────────────────────────────────────────────────┐
  │                 tailscale-gui process                  │
  │                                                        │
  │  main.go                                               │
  │   ├─ client.Client ──────────────────────────────────► tailscaled
  │   │    tailscale.com/client/local                      (Unix socket)
  │   │    Status/Connect/Disconnect/Prefs/WatchState      /var/run/tailscale/
  │   │    SetExitNode/SetAcceptDNS/PushFile/Ping          tailscaled.sock
  │   │                                                    │
  │   ├─ config.Config                                     │
  │   │    ~/.config/tailscale-gui/config.json             │
  │   │                                                    │
  │   ├─ notify.Notifier                                   │
  │   │    notify-send (best-effort)                       │
  │   │                                                    │
  │   ├─ taildrop.Manager                                  │
  │   │    Watch() polls WaitingFiles every 5 s            │
  │   │    SendFile() streams via PushFile                 │
  │   │                                                    │
  │   ├─ window.Manager                                    │
  │   │    HTTP on 127.0.0.1:random                        │
  │   │    /api/status  /api/connect  /api/ping …          │
  │   │    Dashboard HTML auto-refreshes every 5 s         │
  │   │                                                    │
  │   └─ systray.App  ◄── main thread (required) ────────► D-Bus / AppIndicator
  │        IPN bus → icon/menu updates                     desktop shell
  │        Menu clicks → client calls                      │
  └────────────────────────────────────────────────────────┘
```

---

## Configuration

`~/.config/tailscale-gui/config.json` (created on first run):

```json
{
  "admin_console_url": "https://login.tailscale.com/admin/machines",
  "notifications_enabled": true,
  "taildrop_dir": "~/Downloads/Taildrop",
  "start_minimised": true,
  "poll_interval_sec": 30,
  "status_window_port": 0
}
```

Set `admin_console_url` to your Headscale URL if self-hosting.
Set `status_window_port` to a fixed port if you want a stable bookmark.

---

## Status window

Click **"Open status window…"** in the tray menu. A dashboard opens in your
browser at `http://127.0.0.1:<port>/` and auto-refreshes every 5 seconds.

From the dashboard you can:
- Connect / Disconnect
- See all peers with online/offline status
- Click any peer IP to copy it
- Ping any online peer (shows latency + endpoint)
- Change exit node
- Toggle MagicDNS, subnet routes, shields up

---

## Autostart

```bash
make install
```

Installs the binary to `~/.local/bin/` and registers an XDG autostart entry.

---

## Replacing the icons

Drop 32×32 PNG files into `assets/icons/`:

```
assets/icons/connected.png
assets/icons/disconnected.png
assets/icons/connecting.png
assets/icons/warning.png
```

Then `make build`. The icons are embedded into the binary via `go:embed`.

---

## What to build next

- **Subnet route advertising** — `tailscale.com/client/local` exposes
  `AdvertiseRoutes`; add a submenu to toggle which local subnets this device
  advertises to the tailnet.
- **SSH peer launch** — add an "SSH…" button per-peer in the status window
  that runs `xterm -e ssh <hostname>` (or the user's preferred terminal).
- **Multi-account / user switching** — watch for `ipn.NeedsLogin` and open
  the auth URL from `st.AuthURL` automatically in the browser.
- **Flatpak packaging** — add a `packaging/flatpak/` manifest so the app
  can be distributed via Flathub with sandboxed access to the Tailscale socket.
- **Taildrop receive notifications with "Open file" action** — use
  `gdbus` or `go-notify` to attach an action button to the notification
  that opens the saved file directly.

---

## Licence

MIT. Not affiliated with or endorsed by Tailscale Inc.
