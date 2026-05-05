# X800 User Guide

User manual for the 70mai DR4800/X800 custom firmware and addon installer.

Target audience: Users. For development, see [developer-guide.md](developer-guide.md).

## Overview

The firmware extends the camera with development access and the addon installer. The installer can run directly on the camera on port `8080` or be started locally on your computer and control the camera via SSH, either directly or via the Tailscale IP.

| Area | Default |
|---|---|
| Camera Hotspot | `70mai_X800_...` |
| Camera IP in AP Mode | `192.168.0.1` |
| Addon Installer on Camera | `http://192.168.0.1:8080` |
| SSH | `ssh root@192.168.0.1` |
| Addon Installer Local | `http://127.0.0.1:8765` |

Root typically has no password on the tested images.

## First Connection

1. Turn on the camera.
2. Connect your computer or smartphone to the camera hotspot.
3. Open the addon installer on the camera: `http://192.168.0.1:8080`.
4. Alternatively, start the addon installer on your computer.

Start the addon installer:

```bash
./main.sh build
./main.sh start
```

Then open in your browser:

```text
http://127.0.0.1:8765
```

## Addon Installer

The addon installer is the only current web interface.

| Area | Purpose |
|---|---|
| Connection | Host, SSH port, password, Tailscale peer selection |
| System | Status, network, and processes |
| Files | File browser, read, download, upload to `/mnt/sd` |
| Firmware | Upload firmware via SSH or FTP as `/mnt/sd/FW98530A.bin` |
| WiFi Mode | Client mode, wpalib, main_app wrapper |
| Tailscale | Binary upload, join, exit node, subnet routes, autostart |
| Streaming | RTSP links for external players |
| GPS & Flespi | Live GPS, map, Flespi and Flespi daemon |

AP SSID and AP password from the installer are runtime-only. After a reboot, the vendor app restores values from `mtd7/usr1`.

The sidebar shows the current path:

- `via WiFi (SSH)` or `via Tailscale (SSH)` when the installer works via SSH.
- `Tailscale running` refers to the camera once its status is known. A local Tailscale CLI error on the computer is handled as `Local Tailscale not running`.

## Tailscale

Tailscale is the recommended return channel once the camera is no longer directly in AP mode.

Recommended workflow:

1. Install Tailscale binaries in the addon installer.
2. Start `tailscaled`.
3. Enter authkey and run `tailscale up`.
4. Optionally enable `Remove key after up`.
5. Optionally enable autostart.
6. Optionally deploy and start the Flespi daemon.

If the camera is already in the tailnet, the installer uses `tailscale set` for changes like exit node, accept routes, or subnet routes. No new authkey is required for this.

Subnet routes are automatically detected from the local camera networks and displayed as checkboxes. On the tested camera, these were:

| Interface | Route |
|---|---|
| `wlan0` | `10.90.90.0/23` |
| `usb_4g0` | `192.168.100.0/24` |

## WiFi Client Mode

Client mode connects the camera to an existing WLAN. The camera needs:

- `/mnt/sd/wpalib/`
- `/mnt/sd/wpa_supplicant.conf`
- Persistent `/usr/bin/main_app` wrapper

Recommended workflow in the addon installer:

1. Upload `wpalib`.
2. Install wrapper.
3. Enter SSID/password.
4. Optionally enter static IP.
5. Connect. The camera restarts.
6. Then connect via DHCP IP or Tailscale IP.

Back to AP mode:

1. Run `Disconnect` in the installer, or
2. Remove SD card and delete `/mnt/sd/wpa_supplicant.conf`.

Live finding: WiFi client mode can survive a reset. If the camera does not appear as a `192.168.0.1` hotspot after reset, first check DHCP IP in the router or Tailscale, then selectively remove the client configuration.

The health check in WiFi mode runs on the camera itself. It pings `8.8.8.8`, `1.1.1.1`, the detected gateway, and optionally the DNS server.

On boot, the WiFi wrapper first checks via `wpa_cli` whether the camera is actually associated with the target WLAN. If this fails after about 30 seconds, it goes directly back to AP mode. If the association works but DHCP, default route, or ping to the outside permanently fail, it backs up `/mnt/sd/wpa_supplicant.conf` and `network.conf` as `*.failed-<timestamp>` and also reboots back to AP mode. Log: `/mnt/sd/wifi-client-health.log`.

## GPS and Map

The installer reads GPS in this order:

1. `/mnt/sd/gps_state.txt`
2. Internal CASIC module `/dev/ttyS1` with `115200` baud, NMEA `$GN*`
3. Conservative fallback TTYs

`/dev/ttyUSB6` is a GNSS source on the UP04/Quectel modem, but does not deliver direct NMEA for installer evaluation on the tested device. It is displayed in debug but not parsed as NMEA.

In the tracking area there are:

- `Load`: Query current position.
- `Debug`: Raw samples from `gps_state.txt`, `/dev/ttyS1@115200`, `/dev/ttyUSB6@9600`.
- `Live Console`: GPS query every 2.5 seconds.
- Map with OpenStreetMap if a fix is available.

## Flespi

Flespi uses a locally stored token. The token is only written to the SD card if you consciously activate it in the UI.

Important:

- Flespi device ID must be numeric.
- Values like `ftp` are protocol/channel names, not device IDs.
- If no device ID is set, the installer can search for or create an HTTP device via Flespi API.
- The camera daemon sends via `curl` if available, otherwise via BusyBox `wget`.
- On HTTP **400** (parameters do not match device type), the daemon automatically tries slimmer payloads: metrics+GPS → only GPS+basic data → only timestamp/ident. Optionally, `ident` from the installer is also delivered in `flespi.conf` (`FLESPI_IDENT`) on the SD.
- Metrics include free **RAM** (`memory.free`, kB) and free space on **`/mnt/sd`** (`filesystem.sd.available`, kB according to `df -P`, column "Available"). The daemon builds JSON with helper functions (`json_esc_str` / `json_uint` / `json_load`) so that **strings** and **numbers** remain valid (e.g., line breaks in `ident`, otherwise invalid JSON).

**Message format (verification):** Flespi documentation and KB show the **HTTP channel** like this (own port, device detection e.g., via `vehicle_id` in JSON):

```bash
curl -X POST -d '[{"vehicle_id":"1234", "timestamp":1234567890, "position.latitude":52, "position.longitude":48, "position.speed":55, "position.direction":75, "battery.level":87}]' https://gw.flespi.io:27699
```

(Example as in Flespi KB, only with **`battery.level`** instead of `fuel.level`.)

The **installer daemon** uses the same **message layout**: a **JSON array** with **exactly one object**, parameters with **dot notation** (`position.latitude`, …), **`timestamp`** as a number — but the **REST endpoint for an already created device** (device is selected via the URL, not via the channel port):

```bash
curl -X POST \
  -H "Authorization: FlespiToken YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{"timestamp":1710000000,"ident":"mein-x800","position.latitude":52.1,"position.longitude":48.2}]' \
  "https://flespi.io/gw/devices/12345678/messages"
```

`vehicle_id` in the channel example corresponds functionally to optional **`ident`** in the JSON (if set in `flespi.conf`); the **numeric Flespi device ID** is in our **URL** instead of `vehicle_id`.

Logs:

```text
/mnt/sd/flespi-daemon.log
```

## SD Card

Important files:

| Path | Purpose |
|---|---|
| `FW98530A.bin` | Firmware update on next boot |
| `FW98530A.bin.applied-*` | Already applied update |
| `wpa_supplicant.conf` | Activates WiFi client mode |
| `network.conf` | Optional static client IP |
| `tailscale`, `tailscaled` | Tailscale binaries |
| `tailscale-state` | Tailnet state, do not delete unnecessarily |
| `tailscale-autostart` | Sentinel for autostart |
| `wpalib/` | musl/wpa_supplicant package |
| `flespi.conf` | Flespi token and device ID |
| `customfw.log` | Boot/recovery log |
| `autorun.sh` | Optional custom FW autorun |

Always run `sync` after write operations on the SD.

## Firmware Update

1. Copy build artifact as `FW98530A.bin` to the root of the SD.
2. Do not leave any `._*` AppleDouble files next to it.
3. Insert SD and start the camera.
4. Do not disconnect power during flashing.
5. After successful start, the custom firmware renames `FW98530A.bin` to `FW98530A.bin.applied-*`.
6. Then format the SD card in the camera.
7. Redeploy addon files like Tailscale, WiFi client configuration, or Flespi afterwards.

Correct firmware size for the current base:

```text
141221120 bytes
```

The addon installer checks the SD root after firmware upload. Errors include missing `FW98530A.bin`, incorrect upload size, macOS `._*` files, or additional `.bin`/`FW98530A*` files. On errors, a requested reboot is blocked.

Do not flash significantly smaller files.
