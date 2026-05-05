# 70mai Addon Installer

Browser-basierte Addon-Verwaltung für die **70mai DR4800 / X800 Kamera.

Läuft auf Ihrem PC und verbindet sich per SSH oder Tailscale mit der Kamera – kein App-Wechsel, keine Eingabeaufforderung bei normaler Nutzung erforderlich.

---

## ⭐ WiFi-Client-Modus

Verbinden Sie die Kamera mit Ihrem Heim-WLAN anstelle des Hotspot-Betriebs.

- Scannen Sie nach verfügbaren Netzwerken und schließen Sie sich per Browser-UI an
- Statische IP-Adresse unterstützen
- Automatisch neu verbinden nach Neustart durch dauerhafte `wpa_supplicant`-Konfiguration auf SD-Karte
- Einfaches Trennen der Verbindung und Zurückkehren in den AP-Modus mit einem Klick
- Enthält sein eigenes `wpa_supplicant` + `musl` – keine Installation auf der Kamera erforderlich

---

## Funktionen

| Funktion | Beschreibung |
|---|---|
| **WiFi Client Modus** | Verbindung zur Kamera mit einem beliebigen WPA2-Netzwerk herstellen |
| **Tailscale** | Entfernter Zugriff von überall aus, Subnetz-Routing, Exit-Knoten |
| **GPS Tracking** | Live-Karte, NMEA Debug Konsole, Roh-Daten via serieller Ausgabe |
| **Flespi Telemetrie** | Sendung von GPS + Systemdaten an Ihr Flespi Konto |
| **Firmware-Upload** | Firmware per SSH oder SD-Karte senden |
| **Datei-Zugriff** | Durchsuchen und Herunterladen der Kamera-Dateien per SSH |
| **Log Konsole** | Live Log-Streaming mit Funktionsfilter |

## Anforderungen

- 70mai DR4800 / X800 mit aktivierter SSH (benutzerdefiniertes Firmware)
- Kamera erreichbar über `192.168.0.1` (AP-Modus) oder Tailscale IP
- Go 1.21+ (zum Kompilieren aus dem Quellcode)

---

## Schneller Einstieg

```bash
git clone https://github.com/kpma1985/70mai_addons
cd 70mai_addons
./main.sh build
./main.sh start
```

Öffnen Sie **http://localhost:8765** in Ihrem Browser.

---

## Kamerakonfiguration

1. Schließen Sie Ihren PC mit dem WLAN-Netzwerk der Kamera (`192.168.0.1`) an
2. Geben Sie das SSH-Kennwort im Reiter **Config** ein
3. Klicken Sie auf **Connect** - Der Installer erkennt den Status der Kamera automatisch
4. Verwenden Sie den Tab **WiFi**, um zur Client-Modus zu wechseln

---

## Dokumentation

- [Benutzeranleitung](docs/user-guide.md)
- [Versionshinweise](docs/release-notes.md)

---

## Status

In Arbeit. WiFi-Client-Modus, Tailscale und GPS sind funktionsfähig.
Flespi vollständiger Datenträger und Proxy-ARP Weiterleitungen werden noch getestet.

Feedback und Tests willkommen – Eröffnen Sie ein neues Problem oder schreiben Sie in das Forumsthread.^s