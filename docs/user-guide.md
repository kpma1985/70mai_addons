# X800 User Guide

Bedienungsanleitung für die 70mai DR4800/X800 Custom Firmware und den Addon Installer.

Zielgruppe: Anwender. Für Entwicklung siehe [developer-guide.md](developer-guide.md).

## Überblick

Die Firmware erweitert die Kamera um Entwicklungszugänge und den Addon Installer. Der Installer kann direkt auf der Kamera auf Port `8080` laufen oder lokal auf deinem Rechner gestartet werden und steuert die Kamera per SSH, direkt oder über die Tailscale-IP.

| Bereich | Standard |
|---|---|
| Kamera-Hotspot | `70mai_X800_...` |
| Kamera-IP im AP-Mode | `192.168.0.1` |
| Addon Installer auf der Kamera | `http://192.168.0.1:8080` |
| SSH | `ssh root@192.168.0.1` |
| Addon Installer lokal | `http://127.0.0.1:8765` |

Root hat auf den getesteten Images üblicherweise kein Passwort.

## Erste Verbindung

1. Kamera einschalten.
2. Rechner oder Smartphone mit dem Kamera-Hotspot verbinden.
3. Addon Installer auf der Kamera öffnen: `http://192.168.0.1:8080`.
4. Alternativ den Addon Installer auf dem Rechner starten.

Addon Installer starten:

```bash
./main.sh build
./main.sh start
```

Danach im Browser öffnen:

```text
http://127.0.0.1:8765
```

## Addon Installer

Der Addon Installer ist die einzige aktuelle Weboberfläche.

| Bereich | Zweck |
|---|---|
| Verbindung | Host, SSH-Port, Passwort, Tailscale-Peer-Auswahl |
| System | Status, Netzwerk und Prozesse |
| Dateien | Dateibrowser, Lesen, Download, Upload nach `/mnt/sd` |
| Firmware | Firmware per SSH oder FTP als `/mnt/sd/FW98530A.bin` hochladen |
| WiFi Modus | Client-Mode, wpalib, main_app-Wrapper |
| Tailscale | Binary-Upload, Join, Exit Node, Subnet Routes, Autostart |
| Streaming | RTSP-Links für externe Player |
| GPS & Flespi | Live-GPS, Karte, Flespi und Flespi-Daemon |

AP-SSID und AP-Passwort aus dem Installer sind runtime-only. Nach einem Reboot stellt die Vendor-App Werte aus `mtd7/usr1` wieder her.

Die Sidebar zeigt den aktuellen Pfad:

- `via WiFi (SSH)` oder `via Tailscale (SSH)` wenn der Installer per SSH arbeitet.
- `Tailscale running` bezieht sich auf die Kamera, sobald deren Status bekannt ist. Ein lokaler Tailscale-CLI-Fehler auf dem Rechner wird als `Local Tailscale not running` behandelt.

## Tailscale

Tailscale ist der empfohlene Rückkanal, sobald die Kamera nicht mehr direkt im AP-Mode hängt.

Empfohlener Ablauf:

1. Im Addon Installer Tailscale-Binaries installieren.
2. `tailscaled` starten.
3. Authkey eintragen und `tailscale up` ausführen.
4. Optional `Key nach Up entfernen` aktivieren.
5. Optional Autostart aktivieren.
6. Optional Flespi-Daemon deployen und starten.

Wenn die Kamera bereits im Tailnet ist, nutzt der Installer für Änderungen wie Exit Node, Accept Routes oder Subnet Routes `tailscale set`. Dafür ist kein neuer Authkey nötig.

Subnet Routes werden automatisch aus den lokalen Kamera-Netzen erkannt und als Checkboxen angezeigt. Auf der getesteten Kamera waren das:

| Interface | Route |
|---|---|
| `wlan0` | `10.90.90.0/23` |
| `usb_4g0` | `192.168.100.0/24` |

## WiFi-Client-Modus

Der Client-Mode verbindet die Kamera mit einem vorhandenen WLAN. Dafür braucht die Kamera:

- `/mnt/sd/wpalib/`
- `/mnt/sd/wpa_supplicant.conf`
- persistenten `/usr/bin/main_app`-Wrapper

Empfohlener Ablauf im Addon Installer:

1. `wpalib` hochladen.
2. Wrapper installieren.
3. SSID/Passwort eintragen.
4. Optional statische IP eintragen.
5. Verbinden. Die Kamera startet neu.
6. Danach über DHCP-IP oder Tailscale-IP verbinden.

Zurück in den AP-Mode:

1. Im Installer `Trennen` ausführen, oder
2. SD-Karte entnehmen und `/mnt/sd/wpa_supplicant.conf` löschen.

Live-Befund: Der WiFi-Client-Mode kann auch einen Reset überleben. Wenn die Kamera nach Reset nicht als `192.168.0.1`-Hotspot erscheint, zuerst DHCP-IP im Router oder Tailscale prüfen und dann die Client-Konfiguration gezielt entfernen.

Der Health-Check im WiFi-Modus läuft auf der Kamera selbst. Er pingt `8.8.8.8`, `1.1.1.1`, das erkannte Gateway und optional den DNS-Server.

Beim Boot prüft der WiFi-Wrapper zuerst per `wpa_cli`, ob die Kamera überhaupt mit dem Ziel-WLAN assoziiert ist. Scheitert das nach ca. 30 Sekunden, geht er direkt zurück in den AP-Modus. Wenn die Association klappt, aber DHCP, Default-Route oder Ping nach außen dauerhaft scheitern, sichert er `/mnt/sd/wpa_supplicant.conf` und `network.conf` als `*.failed-<zeit>` und rebootet ebenfalls zurück in den AP-Modus. Log: `/mnt/sd/wifi-client-health.log`.

## GPS und Karte

Der Installer liest GPS in dieser Reihenfolge:

1. `/mnt/sd/gps_state.txt`
2. internes CASIC-Modul `/dev/ttyS1` mit `115200` Baud, NMEA `$GN*`
3. konservative Fallback-TTYs

`/dev/ttyUSB6` ist beim UP04/Quectel-Modem eine GNSS-Quelle, liefert aber auf dem getesteten Gerät kein direktes NMEA für die Installer-Auswertung. Es wird im Debug angezeigt, aber nicht als NMEA geparst.

Im Tracking-Bereich gibt es:

- `Laden`: aktuelle Position abfragen.
- `Debug`: Rohproben aus `gps_state.txt`, `/dev/ttyS1@115200`, `/dev/ttyUSB6@9600`.
- `Live-Konsole`: GPS-Abfrage alle 2,5 Sekunden.
- Karte mit OpenStreetMap, wenn ein Fix vorhanden ist.

## Flespi

Flespi nutzt einen lokal gespeicherten Token. Der Token wird nur dann auf die SD geschrieben, wenn du das bewusst im UI aktivierst.

Wichtig:

- Flespi Device-ID muss numerisch sein.
- Werte wie `ftp` sind Protokoll-/Kanalnamen, keine Device-ID.
- Wenn keine Device-ID gesetzt ist, kann der Installer per Flespi-API ein HTTP-Gerät suchen oder anlegen.
- Der Kamera-Daemon sendet per `curl`, falls vorhanden, sonst per BusyBox-`wget`.
- Bei HTTP **400** (Parameter passen nicht zum Gerätetyp) versucht der Daemon automatisch schlankere Payloads: Metriken+GPS → nur GPS+Grunddaten → nur Timestamp/ident. Optional wird `ident` aus dem Installer in `flespi.conf` (`FLESPI_IDENT`) auf der SD mit ausgeliefert.
- Metriken enthalten u. a. freien **RAM** (`memory.free`, kB) und freien Platz auf **`/mnt/sd`** (`filesystem.sd.available`, kB laut `df -P`, Spalte „Available“). Der Daemon baut JSON mit Hilfsfunktionen (`json_esc_str` / `json_uint` / `json_load`), damit **Strings** und **Zahlen** valide bleiben (z. B. Zeilenumbrüche in `ident`, sonst ungültiges JSON).

**Nachrichtenformat (Verifikation):** Flespi-Dokumentation und KB zeigen den **HTTP-Kanal** so (eigener Port, Erkennung des Geräts z. B. über `vehicle_id` im JSON):

```bash
curl -X POST -d '[{"vehicle_id":"1234", "timestamp":1234567890, "position.latitude":52, "position.longitude":48, "position.speed":55, "position.direction":75, "battery.level":87}]' https://gw.flespi.io:27699
```

(Beispiel wie in der Flespi-KB, nur mit **`battery.level`** statt `fuel.level`.)

Der **Installer-Daemon** nutzt dasselbe **Nachrichtenlayout**: ein **JSON-Array** mit **genau einem Objekt**, Parameter mit **Punkt-Notation** (`position.latitude`, …), **`timestamp`** als Zahl — aber den **REST-Endpunkt für ein bereits angelegtes Gerät** (Gerät wird über die URL gewählt, nicht über den Kanal-Port):

```bash
curl -X POST \
  -H "Authorization: FlespiToken YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '[{"timestamp":1710000000,"ident":"mein-x800","position.latitude":52.1,"position.longitude":48.2}]' \
  "https://flespi.io/gw/devices/12345678/messages"
```

`vehicle_id` im Kanalbeispiel entspricht funktional optional **`ident`** im JSON (wenn in `flespi.conf` gesetzt); die **numerische Flespi-Device-ID** steht bei uns in der **URL** statt in `vehicle_id`.

Logs:

```text
/mnt/sd/flespi-daemon.log
```

## SD-Karte

Wichtige Dateien:

| Pfad | Zweck |
|---|---|
| `FW98530A.bin` | Firmware-Update beim nächsten Boot |
| `FW98530A.bin.applied-*` | bereits angewendetes Update |
| `wpa_supplicant.conf` | aktiviert WiFi-Client-Mode |
| `network.conf` | optionale statische Client-IP |
| `tailscale`, `tailscaled` | Tailscale-Binaries |
| `tailscale-state` | Tailnet-State, nicht unnötig löschen |
| `tailscale-autostart` | Sentinel für Autostart |
| `wpalib/` | musl/wpa_supplicant-Paket |
| `flespi.conf` | Flespi-Token und Device-ID |
| `customfw.log` | Boot-/Recovery-Log |
| `autorun.sh` | optionaler Custom-FW-Autorun |

Nach Schreibvorgängen auf der SD immer `sync` ausführen.

## Firmware-Update

1. Build-Artefakt als `FW98530A.bin` ins Root der SD kopieren.
2. Keine `._*` AppleDouble-Dateien daneben liegen lassen.
3. SD einlegen und Kamera starten.
4. Während des Flashens Strom nicht trennen.
5. Nach erfolgreichem Start benennt die Custom-FW `FW98530A.bin` zu `FW98530A.bin.applied-*` um.
6. Danach die SD-Karte in der Kamera formatieren.
7. Addon-Dateien wie Tailscale, WiFi-Client-Konfiguration oder Flespi danach neu deployen.

Korrekte Firmware-Größe für die aktuelle Base-Basis:

```text
141221120 Bytes
```

Der Addon Installer prüft nach Firmware-Upload das SD-Root. Fehler sind z. B. fehlende `FW98530A.bin`, falsche Upload-Größe, macOS-`._*`-Dateien oder zusätzliche `.bin`/`FW98530A*`-Dateien. Bei Fehlern wird ein angeforderter Reboot blockiert.

Deutlich kleinere Dateien nicht flashen.
