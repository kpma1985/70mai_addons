# Developer Guide

Kurzreferenz für Build, Tests und Layout des **X800 Addon Installers** (Quellcode im Repository-Root).

## Voraussetzungen

- **Go** 1.21+
- optional **Node.js** für JS-Syntax- und i18n-Checks

## Build & Start

```bash
./main.sh build
./main.sh start
```

Standard-URL: `http://127.0.0.1:8765`

## Tests & Qualität

```bash
go test ./...
node --check web/app.js
node --check web/i18n.js
node --check web/sidebar.js
node scripts/check-i18n.js
```

## Verzeichnisüberblick

| Pfad | Inhalt |
|------|--------|
| `main.go`, `go.mod` | Einstieg, eingebettete `web/` |
| `internal/api/` | HTTP-API, Handler |
| `internal/config/` | Lokale `installer.json` |
| `internal/ops/` | Remote-Skripte (SSH auf die Kamera) |
| `web/` | Statische UI (`index.html`, `app.js`, `i18n.js`) |
| `bin/wpalib/` | Eingebettete WiFi-Binary-Bündel (`go:embed`) |

## Konfiguration auf dem Rechner

Standard: `~/.config/x800_addon/installer.json` — enthält u. a. Zugangsdaten, Tailscale- und Flespi-Felder. Nicht ins Repo committen.

## Nicht im Repository

- **Tailscale-Binaries** werden zur Laufzeit von pkgs.tailscale.com geladen (Cache unter der Benutzer-Home-Konfiguration).
- **Firmware-Images** (`*.bin`) sind per `.gitignore` ausgeschlossen.

Siehe auch [README.md](../README.md) und [user-guide.md](user-guide.md).
