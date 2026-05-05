# Hardware-Kontext (DR4800 / X800)

Kurzüberblick für Entwicklung und Support — keine vollständige Spezifikation.

## Netzwerk (typisch)

| Thema | Wert / Hinweis |
|--------|----------------|
| AP-Mode IP | `192.168.0.1` |
| SSH | `root@<Kamera-IP>`, Port **22** (sofern Custom-Firmware aktiv) |
| Telnet-Fallback | Port **23** (eingeschränkt im Installer) |
| RTSP | Port **554**, typische Pfade u. a. `livestream/12`, `liveRTSP/av4` |

## Speicher / Persistenz

- **SD-Karte** unter `/mnt/sd/` — u. a. `wpa_supplicant.conf`, Tailscale-State, Flespi-Dateien, Firmware `FW98530A.bin` im Root zum Flashen.
- Vendor-spezifische Persistenz von AP-SSID/-Passphrase über **`mtd7` / `usr1`** (Details siehe Nutzer- und Release-Doku).

## Mobilfunk / Routing

- Bei eingebautem LTE können zusätzliche Interfaces (z. B. `usb_4g0`) und andere private Netze auftauchen — nicht für alle Setups mit `192.168.0.0/24` gleichsetzen.
