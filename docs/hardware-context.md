# Hardware Context (DR4800 / X800)

Brief overview for development and support — not a complete specification.

## Network (typical)

| Topic | Value / Note |
|--------|----------------|
| AP Mode IP | `192.168.0.1` |
| SSH | `root@<camera-IP>`, port **22** (if custom firmware active) |
| Telnet fallback | port **23** (limited in installer) |
| RTSP | port **554**, typical paths include `livestream/12`, `liveRTSP/av4` |

## Storage / Persistence

- **SD card** under `/mnt/sd/` — includes `wpa_supplicant.conf`, Tailscale state, Flespi files, firmware `FW98530A.bin` in root for flashing.
- Vendor-specific persistence of AP SSID/passphrase via **`mtd7` / `usr1`** (details see user and release docs).

## Cellular / Routing

- With built-in LTE, additional interfaces (e.g., `usb_4g0`) and other private networks may appear — do not equate all setups with `192.168.0.0/24`.
