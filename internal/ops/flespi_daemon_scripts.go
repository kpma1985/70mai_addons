package ops

import (
	"fmt"
	"strings"
)

func normalizeFlespiInterval(interval int) int {
	if interval <= 0 {
		return 30
	}
	if interval < 5 {
		return 5
	}
	if interval > 3600 {
		return 3600
	}
	return interval
}

// FlespiDaemonInstallScript deploys flespi-daemon.sh to the camera.
// The daemon runs in the background, reads GPS + system metrics, and sends them via curl
// to POST /gw/devices/{device_id}/messages.
//
// Message body matches the same flespi stream format as the HTTP channel example: a JSON array
// of one object, flespi dot-notation parameters (e.g. position.latitude), numeric timestamp.
// The KB uses POST https://gw.flespi.io:PORT with vehicle_id/ident; we use the device REST path
// with Authorization: FlespiToken and the numeric device id in the URL. Full tier adds RAM + SD
// free KB from /proc/meminfo and df -P /mnt/sd (filesystem.sd.available).
// Empty token/deviceID: writes placeholder values into flespi.conf. The host defaults to
// the public Flespi endpoint if not provided.
func FlespiDaemonInstallScript(token, deviceID, ident, host string, interval int, autostart bool) string {
	token = strings.TrimSpace(token)
	deviceID = strings.TrimSpace(deviceID)
	ident = strings.TrimSpace(ident)
	host = strings.TrimSpace(host)
	if host == "" {
		host = "https://flespi.io"
	}
	host = strings.TrimRight(host, "/")
	interval = normalizeFlespiInterval(interval)

	var confCmd string
	if token != "" {
		confCmd = fmt.Sprintf(
			"printf 'FLESPI_TOKEN=%s\\nFLESPI_DEVICE_ID=%s\\nFLESPI_IDENT=%s\\nFLESPI_HOST=%s\\n' > /mnt/sd/flespi.conf",
			shQuoteSingle(token), shQuoteSingle(deviceID), shQuoteSingle(ident), shQuoteSingle(host),
		)
	} else {
		confCmd = fmt.Sprintf(
			"printf 'FLESPI_TOKEN=\\nFLESPI_DEVICE_ID=\\nFLESPI_IDENT=\\nFLESPI_HOST=%s\\n' > /mnt/sd/flespi.conf",
			shQuoteSingle(host),
		)
	}
	autostartCmd := `rm -f /mnt/sd/flespi-autostart 2>/dev/null || true`
	if autostart {
		autostartCmd = `touch /mnt/sd/flespi-autostart`
	}

	return `#!/bin/sh
set -e
` + confCmd + `
` + autostartCmd + `

cat > /mnt/sd/flespi-daemon.sh << 'DAEMON_EOF'
#!/bin/sh
# Flespi Telemetrie-Daemon — GPS + Systemmetriken an Flespi senden.
# Body: JSON-Array mit einem Objekt (wie flespi-Doku: curl -d '[{...}]' .../messages).
# Voraussetzung: /mnt/sd/flespi.conf mit FLESPI_TOKEN und FLESPI_DEVICE_ID.
CONF=/mnt/sd/flespi.conf
INTERVAL=` + fmt.Sprintf("%d", interval) + `
LOG=/mnt/sd/flespi-daemon.log
PATH=/bin:/sbin:/usr/bin:/usr/sbin
export PATH

log() { printf '[%s] %s\n' "$(date '+%H:%M:%S' 2>/dev/null || echo '?')" "$*" >> "$LOG" 2>/dev/null || true; }

# JSON-String escapen (\ " Zeilenumbruch); Fallback ohne awk: nur \ " und CR/LF entfernen
json_esc_str() {
  if command -v awk >/dev/null 2>&1; then
    printf '%s' "$1" | awk '{ gsub(/\\/,"\\\\"); gsub(/"/,"\\\""); gsub(/\r/,""); gsub(/\n/,"\\n"); printf "%s",$0 }'
  else
    printf '%s' "$1" | sed 's/\\/\\\\/g;s/"/\\"/g' | tr -d '\r\n'
  fi
}

# Nur nicht-negative Ganzzahl für JSON (timestamp, KB, uptime)
json_uint() {
  case "$1" in ''|*[!0-9]*) printf '0' ;; *) printf '%s' "$1" ;; esac
}

# loadavg: erste Zahl (awk), sonst Trim bis erstes Leerzeichen
json_load() {
  if command -v awk >/dev/null 2>&1; then
    _l=$(printf '%s' "$1" | awk '{print $1}')
  else
    _l=$(printf '%s' "$1" | sed 's/[[:space:]].*//')
  fi
  case "$_l" in ''|*[!0-9.-]*|'-'|'.'|'-.') printf '0' ;; *) printf '%s' "$_l" ;; esac
}

[ -r "$CONF" ] && . "$CONF"
[ -n "$FLESPI_TOKEN" ]    || { log "FLESPI_TOKEN fehlt in $CONF"; exit 1; }
[ -n "$FLESPI_DEVICE_ID" ] || { log "FLESPI_DEVICE_ID fehlt in $CONF"; exit 1; }
case "$FLESPI_DEVICE_ID" in
  *[!0-9]*)
    log "FLESPI_DEVICE_ID ungültig: '$FLESPI_DEVICE_ID' ist keine numerische Device-ID"
    exit 1
    ;;
esac

ENDPOINT_BASE=${FLESPI_HOST:-https://flespi.io}
ENDPOINT="${ENDPOINT_BASE}/gw/devices/${FLESPI_DEVICE_ID}/messages"

log "Daemon gestartet (PID $$, Interval ${INTERVAL}s, Device ${FLESPI_DEVICE_ID})"

while true; do
  # GPS aus gps_state.txt (lat lon time)
  GPS_LAT=""; GPS_LON=""; GPS_TIME=""
  if [ -r /mnt/sd/gps_state.txt ]; then
    read GPS_LAT GPS_LON GPS_TIME < /mnt/sd/gps_state.txt 2>/dev/null || true
  fi

  # Systemmetriken
  TIMESTAMP=$(json_uint "$(date +%s 2>/dev/null || echo 0)")
  read UPTIME_RAW _ < /proc/uptime 2>/dev/null || UPTIME_RAW=0
  UPTIME=${UPTIME_RAW%%.*}
  UPTIME=$(json_uint "$UPTIME")
  read LOAD1 _ < /proc/loadavg 2>/dev/null || LOAD1=0
  LOAD_JSON=$(json_load "$LOAD1")
  MEM_FREE=0
  MEM_TOTAL=1
  while read K V _; do
    [ "$K" = "MemAvailable:" ] && MEM_FREE=$V
    [ "$K" = "MemTotal:" ] && MEM_TOTAL=$V
  done < /proc/meminfo 2>/dev/null || true
  MEM_FREE=$(json_uint "$MEM_FREE")
  MEM_TOTAL=$(json_uint "$MEM_TOTAL")
  [ "$MEM_TOTAL" -eq 0 ] 2>/dev/null && MEM_TOTAL=1

  # Freier Platz auf SD (/mnt/sd), wie df „Available“ — KiB (POSIX df -P)
  SD_FREE_KB=0
  if command -v df >/dev/null 2>&1; then
    SD_FREE_KB=$(df -P /mnt/sd 2>/dev/null | awk 'NR==2 {print $4}')
  fi
  SD_FREE_KB=$(json_uint "$SD_FREE_KB")

  # CORE: optional ident (wie HTTP-Protokoll) + timestamp
  if [ -n "$FLESPI_IDENT" ]; then
    ESC_IDENT=$(json_esc_str "$FLESPI_IDENT")
    CORE="\"ident\":\"${ESC_IDENT}\",\"timestamp\":${TIMESTAMP}"
  else
    CORE="\"timestamp\":${TIMESTAMP}"
  fi

  # GPS — Standard-Flespi-Keys; Metriken: device.uptime, cpu.load, memory.free (kB), filesystem.sd.available (kB frei /mnt/sd)
  GPS_APPEND=""
  HAS_GPS=false
  if [ -n "$GPS_LAT" ] && [ -n "$GPS_LON" ]; then
    case "${GPS_LAT}:${GPS_LON}" in
      *[!0-9.:-]*)
        ;;
      *)
        HAS_GPS=true
        GPS_TIME_ESC=$(json_esc_str "$GPS_TIME")
        GPS_APPEND=",\\\"position.latitude\\\":${GPS_LAT},\\\"position.longitude\\\":${GPS_LON}"
        if [ -n "$GPS_TIME" ]; then
          GPS_APPEND="${GPS_APPEND},\\\"position.gps_time\\\":\\\"${GPS_TIME_ESC}\\\""
        fi
        ;;
    esac
  fi

  METRICS_APPEND=",\\\"device.uptime\\\":${UPTIME},\\\"cpu.load\\\":${LOAD_JSON},\\\"memory.free\\\":${MEM_FREE},\\\"filesystem.sd.available\\\":${SD_FREE_KB}"

  FULL_INNER="${CORE}${GPS_APPEND}${METRICS_APPEND}"
  MID_INNER="${CORE}${GPS_APPEND}"
  MIN_INNER="${CORE}"

  SKIP_MID=false
  [ "$HAS_GPS" = false ] && SKIP_MID=true

  [ "$MEM_TOTAL" -gt 0 ] 2>/dev/null && MEM_USED_KB=$(( MEM_TOTAL - MEM_FREE )) || MEM_USED_KB=0

  BODY_FILE=/tmp/flespi-body-$$.txt
  ERR_FILE=/tmp/flespi-http-$$.err
  PAYLOAD_FILE=/tmp/flespi-payload-$$.json

  ABORT_CYCLE=false
  for tier in 1 2 3; do
    [ "$tier" = "2" ] && [ "$SKIP_MID" = "true" ] && continue
    case "$tier" in
      1) INNER="$FULL_INNER" ;;
      2) INNER="$MID_INNER" ;;
      3) INNER="$MIN_INNER" ;;
    esac

    # Ein gültiges JSON-Objekt im Array: [{"…"}] — ohne trailing commas
    printf '%s' '[{' > "$PAYLOAD_FILE"
    printf '%s' "$INNER" >> "$PAYLOAD_FILE"
    printf '%s' '}]' >> "$PAYLOAD_FILE"
    PAYLOAD_PREVIEW=$(head -c 180 "$PAYLOAD_FILE" 2>/dev/null | sed 's/"/\\"/g')

    CODE=""
    BODY=""
    : > "$BODY_FILE" 2>/dev/null || true
    : > "$ERR_FILE" 2>/dev/null || true
    if command -v curl >/dev/null 2>&1; then
      HTTP=$(curl -sS -w '\n%{http_code}' --connect-timeout 10 \
        -X POST "$ENDPOINT" \
        -H "Authorization: FlespiToken ${FLESPI_TOKEN}" \
        -H "Content-Type: application/json" \
        --data-binary "@${PAYLOAD_FILE}" 2>"$ERR_FILE")
      RC=$?
      CODE=$(printf '%s' "$HTTP" | tail -1)
      BODY=$(printf '%s' "$HTTP" | sed '$d')
    elif command -v wget >/dev/null 2>&1; then
      wget -S -O "$BODY_FILE" --post-file="$PAYLOAD_FILE" \
        --header="Authorization: FlespiToken ${FLESPI_TOKEN}" --header="Content-Type: application/json" \
        -T 10 "$ENDPOINT" 2>"$ERR_FILE"
      RC=$?
      # Busybox wget may not have CA certs. If it fails with a cert error, log and retry without cert check.
      if [ "$RC" != "0" ] && grep -q -E 'certificate|TLS|HTTPS' "$ERR_FILE" 2>/dev/null; then
        log "WARN wget cert validation failed, retrying with --no-check-certificate"
        : > "$ERR_FILE"
        wget -S -O "$BODY_FILE" --post-file="$PAYLOAD_FILE" \
          --header="Authorization: FlespiToken ${FLESPI_TOKEN}" \
          --header="Content-Type: application/json" \
          --no-check-certificate -T 10 "$ENDPOINT" 2>"$ERR_FILE"
        RC=$?
      fi
      CODE=$(grep -E '^[[:space:]]*HTTP/[0-9.]+[[:space:]]+[0-9]+' "$ERR_FILE" 2>/dev/null | tail -1 | awk '{print $2}')
      BODY=$(cat "$BODY_FILE" 2>/dev/null || true)
    else
      log "FEHLER kein HTTP-Client: weder curl noch wget vorhanden; Flespi-Daemon kann nicht senden"
      rm -f "$BODY_FILE" "$ERR_FILE" "$PAYLOAD_FILE"
      exit 1
    fi

    if [ "$RC" != "0" ]; then
      ERR=$(head -3 "$ERR_FILE" 2>/dev/null | tr '\n' ' ' | sed 's/"/\\"/g')
      BODY_ONE=$(cat "$BODY_FILE" 2>/dev/null | head -c 180 | tr '\n' ' ' | sed 's/"/\\"/g')
      log "FEHLER HTTP-Client rc=${RC} code=${CODE:-?}: ${ERR:-keine Details} body=${BODY_ONE:-keine Antwort} payload=${PAYLOAD_PREVIEW} tier=${tier} endpoint=${ENDPOINT}"
      ABORT_CYCLE=true
      break
    fi

    BODY_TRIM=$(printf '%s' "$BODY" | head -c 240 | tr '\n' ' ' | sed 's/"/\\"/g')

    if [ "$CODE" = "200" ] || [ "$CODE" = "204" ]; then
      case "$tier" in
        1) log "OK HTTP ${CODE} tier=full device=${FLESPI_DEVICE_ID} lat=${GPS_LAT:-n/a} lon=${GPS_LON:-n/a} uptime=${UPTIME} load=${LOAD_JSON} mem_free_kb=${MEM_FREE} mem_used_kb=${MEM_USED_KB} sd_avail_kb=${SD_FREE_KB}" ;;
        2) log "OK HTTP ${CODE} tier=gps device=${FLESPI_DEVICE_ID} lat=${GPS_LAT:-n/a} lon=${GPS_LON:-n/a} (Metriken von Flespi abgelehnt)" ;;
        3) log "OK HTTP ${CODE} tier=min device=${FLESPI_DEVICE_ID} (nur timestamp/ident)" ;;
      esac
      break
    fi

    if [ "$CODE" = "400" ] && [ "$tier" -lt 3 ]; then
      log "WARN HTTP 400 tier=${tier}: ${BODY_TRIM:-keine Antwort} — vereinfache Payload"
      continue
    fi

    log "FEHLER HTTP ${CODE}: ${BODY_TRIM:-keine Antwort} payload=${PAYLOAD_PREVIEW} tier=${tier} endpoint=${ENDPOINT}"
    break
  done

  rm -f "$BODY_FILE" "$ERR_FILE" "$PAYLOAD_FILE"

  if [ "$ABORT_CYCLE" = true ]; then
    sleep "$INTERVAL"
    continue
  fi

  sleep "$INTERVAL"
done
DAEMON_EOF
chmod +x /mnt/sd/flespi-daemon.sh
sync
echo '{"ok":true,"msg":"flespi-daemon.sh und flespi.conf unter /mnt/sd/ deployt"}'
`
}

// FlespiDeployScript installs the telemetry daemon and its config.
func FlespiDeployScript(token, deviceID, ident, host string, interval int, autostart bool) string {
	return FlespiDaemonInstallScript(token, deviceID, ident, host, interval, autostart)
}

// FlespiDaemonStartScript startet den Flespi-Daemon als nohup-Hintergrundprozess.
const FlespiDaemonStartScript = `#!/bin/sh
[ -x /mnt/sd/flespi-daemon.sh ] || { echo '{"ok":false,"error":"flespi-daemon.sh fehlt — zuerst Deploy"}'; exit 0; }
daemon_pids() {
  ps 2>/dev/null | awk -v self="$$" '$1 != self && $0 ~ /[s]h[[:space:]]+\/mnt\/sd\/flespi-daemon\.sh/ {print $1}'
}
if [ -n "$(daemon_pids)" ]; then
  echo '{"ok":true,"msg":"flespi-daemon läuft bereits","running":true}'; exit 0
fi
nohup sh /mnt/sd/flespi-daemon.sh >/dev/null 2>&1 &
echo $! > /mnt/sd/flespi-daemon.pid 2>/dev/null || true
sleep 1
if [ -n "$(daemon_pids)" ]; then
  echo '{"ok":true,"msg":"flespi-daemon gestartet","running":true}'
else
  echo '{"ok":false,"error":"flespi-daemon Start fehlgeschlagen — Log: /mnt/sd/flespi-daemon.log","running":false}'
fi
`

// FlespiCameraConfigResetScript stops the daemon, removes autostart and deployed scripts, clears flespi.conf on SD.
const FlespiCameraConfigResetScript = `#!/bin/sh
daemon_pids() {
  ps 2>/dev/null | awk -v self="$$" '$1 != self && $0 ~ /[s]h[[:space:]]+\/mnt\/sd\/flespi-daemon\.sh/ {print $1}'
}
for p in $(daemon_pids); do kill "$p" 2>/dev/null || true; done
rm -f /mnt/sd/flespi-daemon.pid 2>/dev/null || true
sleep 1
rm -f /mnt/sd/flespi-autostart 2>/dev/null || true
printf '%s\n' 'FLESPI_TOKEN=' 'FLESPI_DEVICE_ID=' 'FLESPI_IDENT=' > /mnt/sd/flespi.conf
rm -f /mnt/sd/flespi-daemon.sh /mnt/sd/flespi-ping.sh 2>/dev/null || true
echo '{"ok":true,"msg":"Flespi auf SD zurückgesetzt (Daemon gestoppt, Skripte entfernt, flespi.conf geleert)"}'
`

// FlespiDaemonStopScript beendet den Flespi-Daemon.
const FlespiDaemonStopScript = `#!/bin/sh
daemon_pids() {
  ps 2>/dev/null | awk -v self="$$" '$1 != self && $0 ~ /[s]h[[:space:]]+\/mnt\/sd\/flespi-daemon\.sh/ {print $1}'
}
for p in $(daemon_pids); do kill "$p" 2>/dev/null || true; done
rm -f /mnt/sd/flespi-daemon.pid 2>/dev/null || true
sleep 1
if [ -n "$(daemon_pids)" ]; then
  echo '{"ok":false,"error":"Daemon läuft noch","running":true}'
else
  echo '{"ok":true,"msg":"flespi-daemon gestoppt","running":false}'
fi
`

// FlespiDaemonStatusScript checks whether the daemon is running and reads the last log entry.
const FlespiDaemonStatusScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
daemon_pids() {
  ps 2>/dev/null | awk -v self="$$" '$1 != self && $0 ~ /[s]h[[:space:]]+\/mnt\/sd\/flespi-daemon\.sh/ {print $1}'
}
RUNNING=false
PID=""
PID=$(daemon_pids | head -1)
[ -n "$PID" ] && RUNNING=true
INSTALLED=false
[ -f /mnt/sd/flespi-daemon.sh ] && INSTALLED=true
AUTOSTART=false
[ -f /mnt/sd/flespi-autostart ] && AUTOSTART=true
INTERVAL=30
if [ -r /mnt/sd/flespi-daemon.sh ]; then
  I=$(sed -n 's/^INTERVAL=//p' /mnt/sd/flespi-daemon.sh 2>/dev/null | head -1 | tr -cd '0-9')
  case "$I" in ""|*[!0-9]*) ;; *) INTERVAL=$I ;; esac
fi
LAST_LOG=""
if [ -r /mnt/sd/flespi-daemon.log ]; then
  LAST_LOG=$(tail -3 /mnt/sd/flespi-daemon.log 2>/dev/null | tr '\n' '|')
fi
CONF_OK=false
DEVICE_OK=false
DEVICE_ID=""
[ -r /mnt/sd/flespi.conf ] && grep -q 'FLESPI_TOKEN=' /mnt/sd/flespi.conf && \
  grep -v '^FLESPI_TOKEN=$' /mnt/sd/flespi.conf | grep -q 'FLESPI_TOKEN=' && CONF_OK=true
if [ -r /mnt/sd/flespi.conf ]; then
  DEVICE_ID=$(grep '^FLESPI_DEVICE_ID=' /mnt/sd/flespi.conf 2>/dev/null | head -1 | cut -d= -f2-)
  case "$DEVICE_ID" in ""|*[!0-9]*) DEVICE_OK=false ;; *) DEVICE_OK=true ;; esac
fi
printf '{"ok":true,"running":%s,"installed":%s,"conf_ok":%s,"device_ok":%s,"device_id":"%s","pid":"%s","autostart":%s,"interval":%s,"last_log":"%s"}\n' \
  "$RUNNING" "$INSTALLED" "$CONF_OK" "$DEVICE_OK" "$(json_escape "$DEVICE_ID")" "$(json_escape "${PID:-}")" "$AUTOSTART" "$INTERVAL" "$(json_escape "$LAST_LOG")"
`
