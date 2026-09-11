# 70mai Addon Installer

Browser-based addon management for the **70mai DR4800 / X800 camera**.

Runs on your PC and connects to the camera via SSH or Tailscale – no app switching, no command line required for normal use.

---

## ⭐ WiFi Client Mode

Connect the camera to your home WiFi instead of using hotspot mode.

- Scan for available networks and connect via browser UI
- Static IP address supported
- Automatically reconnect after reboot via persistent `wpa_supplicant` configuration on SD card
- Easily disconnect and return to AP mode with one click
- Includes its own `wpa_supplicant` + `musl` – no installation on camera required

---

## Features

| Feature | Description |
|---|---|
| **WiFi Client Mode** | Connect camera to any WPA2 network |
| **Tailscale** | Remote access from anywhere, subnet routing, exit node |
| **GPS Tracking** | Live map, NMEA debug console, raw data via serial output |
| **Flespi Telemetry** | Send GPS + system data to your Flespi account |
| **Firmware Upload** | Upload firmware via SSH or SD card |
| **File Access** | Browse and download camera files via SSH |
| **Log Console** | Live log streaming with function filter |

## Requirements

- 70mai DR4800 / X800 with SSH enabled (custom firmware)
- Camera reachable via `192.168.0.1` (AP mode) or Tailscale IP
- Go 1.21+ (for building from source)

---

## Quick Start

```bash
git clone https://github.com/kpma1985/70mai_addons
cd 70mai_addons
./main.sh build
./main.sh start
```

Open **http://localhost:8765** in your browser.

---

## Camera Configuration

1. Connect your PC to the camera's WiFi network (`192.168.0.1`)
2. Enter SSH password in the **Config** tab
3. Click **Connect** - The installer automatically detects the camera status
4. Use the **WiFi** tab to switch to client mode

---

## Documentation

- [User Guide](docs/user-guide.md)
- [Developer Guide](docs/developer-guide.md)
- [Hardware Context](docs/hardware-context.md)

---

## Status

Work in progress. WiFi Client Mode, Tailscale, and GPS are functional.
Flespi full data transmission and proxy ARP forwarding are still being tested.

Feedback and tests welcome – Open an issue or write in the forum thread.