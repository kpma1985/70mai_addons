# Developer Guide

Quick reference for build, tests, and layout of the **X800 Addon Installer** (source code in repository root).

## Prerequisites

- **Go** 1.21+
- Optional **Node.js** for JS syntax and i18n checks

## Build & Start

```bash
./main.sh build
./main.sh start
```

Default URL: `http://127.0.0.1:8765`

## Tests & Quality

```bash
go test ./...
node --check web/app.js
node --check web/i18n.js
node --check web/sidebar.js
node scripts/check-i18n.js
```

## Directory Overview

| Path | Content |
|------|--------|
| `main.go`, `go.mod` | Entry point, embedded `web/` |
| `internal/api/` | HTTP API, handlers |
| `internal/config/` | Local `installer.json` |
| `internal/ops/` | Remote scripts (SSH to camera) |
| `web/` | Static UI (`index.html`, `app.js`, `i18n.js`) |
| `bin/wpalib/` | Embedded WiFi binary bundle (`go:embed`) |

## Configuration on Computer

Default: `~/.config/x800_addon/installer.json` — contains access data, Tailscale and Flespi fields. Do not commit to repo.

## Not in Repository

- **Tailscale binaries** are loaded at runtime from pkgs.tailscale.com (cache under user home configuration).
- **Firmware images** (`*.bin`) are excluded via `.gitignore`.

See also [README.md](../README.md) and [user-guide.md](user-guide.md).
