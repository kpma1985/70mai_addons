package ops

import (
	"fmt"
	"strings"
)

// StatusScript reads status directly from the camera over SSH. The old CGI
// WebGUI is no longer part of the supported runtime.
const StatusScript = `json_escape() {
  printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'
}
IFACE=wlan0
IP=$(ifconfig $IFACE 2>/dev/null | awk '/inet /{print $2}' | cut -d: -f2)
[ -z "$IP" ] && IP=$(ifconfig $IFACE 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
MAC=$(ifconfig $IFACE 2>/dev/null | awk '/ether |HWaddr/{print $2; exit}')
SSID=""
MODE=unknown
case "${IP:-}" in
	192.168.0.1|192.168.0.*|"") : ;;
	*)
		if pgrep wpa_supplicant >/dev/null 2>&1 || [ -f /mnt/sd/wpa_supplicant.conf ] || [ -f /var/run/wpa_supplicant.conf ]; then
			MODE=client
		fi
		;;
esac
if [ "$MODE" = "unknown" ] && pgrep wpa_supplicant >/dev/null 2>&1; then
	MODE=client
fi
if [ "$MODE" = "client" ]; then
	if [ -x /mnt/sd/wpalib/wpa_cli ]; then
		SSID=$(/mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib /mnt/sd/wpalib/wpa_cli -i wlan0 status 2>/dev/null | awk -F= '$1=="ssid"{print $2; exit}')
	fi
	[ -z "$SSID" ] && SSID=$(grep 'ssid=' /mnt/sd/wpa_supplicant.conf /var/run/wpa_supplicant.conf 2>/dev/null | grep -v '#' | head -1 | tr -d '"' | cut -d= -f2-)
elif pgrep hostapd >/dev/null 2>&1 || [ "${IP:-}" = "192.168.0.1" ]; then
	MODE=ap
	SSID=$(grep '^ssid=' /var/run/hostapd.conf 2>/dev/null | head -1 | cut -d= -f2-)
	[ -z "$SSID" ] && SSID=$(grep '^ssid=' /etc/wifiap_wpa2.conf 2>/dev/null | head -1 | cut -d= -f2-)
fi
GPS_LAT=""
GPS_LON=""
GPS_TIME=""
GPS_AVAILABLE=false
if [ -r /mnt/sd/gps_state.txt ]; then
	read GPS_LAT GPS_LON GPS_TIME < /mnt/sd/gps_state.txt 2>/dev/null || true
	GPS_LAT=$(printf '%s' "$GPS_LAT" | tr -d '\r\n')
	GPS_LON=$(printf '%s' "$GPS_LON" | tr -d '\r\n')
	GPS_TIME=$(printf '%s' "$GPS_TIME" | tr -d '\r\n')
fi
[ -n "$GPS_LAT" ] && [ -n "$GPS_LON" ] && GPS_AVAILABLE=true
printf '{"mode":"%s","ssid":"%s","iface":"%s","ip":"%s","mac":"%s","gps_lat":"%s","gps_lon":"%s","gps_time":"%s","gps_available":%s,"fallback":true}\n' \
  "$(json_escape "$MODE")" "$(json_escape "$SSID")" "$(json_escape "$IFACE")" \
  "$(json_escape "${IP:-?}")" "$(json_escape "${MAC:-?}")" \
  "$(json_escape "$GPS_LAT")" "$(json_escape "$GPS_LON")" "$(json_escape "$GPS_TIME")" "$GPS_AVAILABLE"
`

// GPSLiveScript reads the current GPS position from gps_state.txt first, then
// probes the internal CASIC GNSS UART for NMEA RMC/GGA sentences.
const GPSLiveScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
emit() {
  OK=$1; SRC=$2; LAT=$3; LON=$4; TM=$5; RAW=$6; DEVICES=$7
  printf '{"ok":true,"gps_available":%s,"gps_lat":"%s","gps_lon":"%s","gps_time":"%s","source":"%s","devices":"%s","raw_live":"%s"}\n' \
    "$OK" "$(json_escape "$LAT")" "$(json_escape "$LON")" "$(json_escape "$TM")" \
    "$(json_escape "$SRC")" "$(json_escape "$DEVICES")" "$(json_escape "$RAW")"
}
DEVICES=$(ls -1 /dev/ttyS* /dev/ttyUSB* /dev/ttyACM* 2>/dev/null | tr '\n' ' ')

if [ -r /mnt/sd/gps_state.txt ]; then
  read LAT LON TM < /mnt/sd/gps_state.txt 2>/dev/null || true
  if [ -n "$LAT" ] && [ -n "$LON" ]; then
    emit true "gps_state.txt" "$LAT" "$LON" "$TM" "$(cat /mnt/sd/gps_state.txt 2>/dev/null | head -1)" "$DEVICES"
    exit 0
  fi
fi

nmea_to_decimal() {
  awk -v V="$1" -v H="$2" 'BEGIN {
    if (V == "" || H == "") exit 1
    dot = index(V, ".")
    deglen = (dot > 5) ? 3 : 2
    deg = substr(V, 1, deglen) + 0
    min = substr(V, deglen + 1) + 0
    dec = deg + (min / 60.0)
    if (H == "S" || H == "W") dec = -dec
    printf "%.7f", dec
  }'
}

parse_nmea() {
  LINE=$1
  case "$LINE" in
    *RMC*)
      OLDIFS=$IFS; IFS=,; set -- $LINE; IFS=$OLDIFS
      TM=$2; VALID=$3; LATV=$4; LATH=$5; LONV=$6; LONH=$7
      [ "$VALID" = "A" ] || return 1
      ;;
    *GGA*)
      OLDIFS=$IFS; IFS=,; set -- $LINE; IFS=$OLDIFS
      TM=$2; LATV=$3; LATH=$4; LONV=$5; LONH=$6; FIX=$7
      [ -n "$FIX" ] && [ "$FIX" != "0" ] || return 1
      ;;
    *) return 1 ;;
  esac
  LAT=$(nmea_to_decimal "$LATV" "$LATH") || return 1
  LON=$(nmea_to_decimal "$LONV" "$LONH") || return 1
  echo "$LAT $LON $TM"
}

probe_dev() {
  DEV=$1
  BAUD=$2
  [ -r "$DEV" ] || return 1
  stty -F "$DEV" "$BAUD" raw -echo 2>/dev/null || true
  TMP=/tmp/gps_live_$$.txt
  rm -f "$TMP"
  (cat "$DEV" > "$TMP" 2>/dev/null) &
  CPID=$!
  sleep 2
  kill "$CPID" 2>/dev/null || true
  wait "$CPID" 2>/dev/null || true
  LINE=$(tr -d '\000' < "$TMP" 2>/dev/null | grep -m1 -E '^\$(GP|GN)(RMC|GGA),' || true)
  rm -f "$TMP"
  [ -n "$LINE" ] || return 1
  POS=$(parse_nmea "$LINE") || return 1
  echo "$POS|$LINE"
}

for SPEC in /dev/ttyS1:115200; do
  DEV=${SPEC%%:*}
  BAUD=${SPEC#*:}
  [ -e "$DEV" ] || continue
  OUT=$(probe_dev "$DEV" "$BAUD") || continue
  POS=${OUT%%|*}
  RAW=${OUT#*|}
  set -- $POS
  emit true "$DEV@$BAUD" "$1" "$2" "$3" "$RAW" "$DEVICES"
  exit 0
done

USB6_NOTE=""
[ -e /dev/ttyUSB6 ] && USB6_NOTE="; /dev/ttyUSB6 erkannt, aber Quectel-binär und nicht als NMEA geparst"
emit false "gps_state.txt" "" "" "" "kein gps_state.txt-Fix; automatische TTY-Probe deaktiviert, weil der Vendor-TTY-Pfad nvt_set_termios-Warnungen in dmesg auslösen kann${USB6_NOTE}" "$DEVICES"
`

// GPSDebugScript captures bounded raw samples from the known GPS sources for
// console diagnostics. It deliberately does not parse /dev/ttyUSB6 as NMEA.
const GPSDebugScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\000' | sed 's/\\/\\\\/g;s/"/\\"/g' | awk 'BEGIN{ORS="\\n"} {gsub(/\r/,""); print}'; }
sample_dev() {
  DEV=$1
  BAUD=$2
  MODE=$3
  TMP=/tmp/gps_debug_$$.bin
  printf '%s\n' "--- ${DEV}@${BAUD} ${MODE}"
  if [ ! -e "$DEV" ]; then
    printf '%s\n' "missing"
    return 0
  fi
  if [ ! -r "$DEV" ]; then
    printf '%s\n' "not readable"
    return 0
  fi
  stty -F "$DEV" "$BAUD" raw -echo 2>/dev/null || true
  rm -f "$TMP"
  (cat "$DEV" > "$TMP" 2>/dev/null) &
  CPID=$!
  sleep 2
  kill "$CPID" 2>/dev/null || true
  wait "$CPID" 2>/dev/null || true
  if [ ! -s "$TMP" ]; then
    printf '%s\n' "no bytes in 2s"
    rm -f "$TMP"
    return 0
  fi
  printf '%s\n' "[nmea]"
  tr -d '\000' < "$TMP" 2>/dev/null | tr '\r' '\n' | grep -a -E '^\$(GP|GN|PCAS)' | head -12 || true
  printf '%s\n' "[hex/ascii]"
  od -An -tx1 -c -N 256 "$TMP" 2>/dev/null | head -24 || true
  rm -f "$TMP"
}

OUT=/tmp/gps_debug_$$.txt
{
  printf '%s\n' "GPS Debug $(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null || echo now)"
  printf '%s' "devices: "; ls -1 /dev/ttyS* /dev/ttyUSB* /dev/ttyACM* 2>/dev/null | tr '\n' ' '; printf '\n'
  if [ -r /mnt/sd/gps_state.txt ]; then
    printf '%s\n' "--- /mnt/sd/gps_state.txt"
    head -5 /mnt/sd/gps_state.txt 2>/dev/null
  else
    printf '%s\n' "--- /mnt/sd/gps_state.txt"
    printf '%s\n' "missing"
  fi
  sample_dev /dev/ttyS1 115200 "CASIC NMEA"
  sample_dev /dev/ttyUSB6 9600 "Quectel binary, not parsed as NMEA"
} > "$OUT" 2>&1
RAW=$(cat "$OUT" 2>/dev/null)
rm -f "$OUT"
printf '{"ok":true,"raw_debug":"%s"}\n' "$(json_escape "$RAW")"
`

// TailscaleStart starts tailscaled manually (no autostart after reboot).
const TailscaleStart = `set -e
[ -x /mnt/sd/tailscaled ] || { echo '{"ok":false,"error":"/mnt/sd/tailscaled fehlt — zuerst Download"}'; exit 0; }
mkdir -p /var/run/tailscale /dev/net
mknod /dev/net/tun c 10 200 2>/dev/null || true
if pgrep tailscaled >/dev/null 2>&1; then echo '{"ok":true,"msg":"tailscaled läuft bereits"}'; exit 0; fi
/mnt/sd/tailscaled --state=/mnt/sd/tailscale-state --tun=userspace-networking --socket=/var/run/tailscale/tailscaled.sock &
sleep 2
if pgrep tailscaled >/dev/null 2>&1; then echo '{"ok":true,"msg":"tailscaled gestartet (ohne Autostart)"}'; else echo '{"ok":false,"error":"tailscaled Start fehlgeschlagen"}'; fi
`

// TailscaleStop beendet Daemon (best effort).
const TailscaleStop = `killall tailscaled 2>/dev/null; sleep 1; echo '{"ok":true,"msg":"tailscaled gestoppt"}'`

// EthernetEnableScript loads the AX88179 driver and brings eth0 up with DHCP.
const EthernetEnableScript = `#!/bin/sh
set -e
json_escape() { printf '%s' "$1" | tr -d '\\r\\n' | sed 's/\\\\/\\\\\\\\/g;s/\"/\\\\\"/g'; }
echo '{"ok":true,"phase":"prep"}'
for dev in /sys/bus/usb/devices/*/power/control; do
  echo on > "$dev" 2>/dev/null || true
done
echo '{"ok":true,"phase":"autosuspend-off"}'
# Only reload if AX88179 not loaded
if ! lsmod | grep -q ax88179_178a; then
  modprobe ax88179_178a 2>&1 || true
  sleep 3
fi
echo '{"ok":true,"phase":"modprobe"}'
# Wait for eth0 to appear
N=0
while [ ! -e /sys/class/net/eth0 ] && [ "$N" -lt 10 ]; do
  sleep 1
  N=$((N + 1))
done
if [ ! -e /sys/class/net/eth0 ]; then
  echo '{"ok":false,"error":"eth0 not found after modprobe"}'
  exit 1
fi
ifconfig eth0 up
udhcpc -i eth0 -n -q -t 5 -T 3 >/dev/null 2>&1 || true
sleep 2
IP=$(ifconfig eth0 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
[ -z "$IP" ] && IP=$(ifconfig eth0 2>/dev/null | awk '/inet /{print $2}')
MAC=$(ifconfig eth0 2>/dev/null | awk '/HWaddr/{print $5; exit}' || ifconfig eth0 2>/dev/null | awk '/ether/{print $2; exit}')
echo "{\\\"ok\\\":true,\\\"phase\\\":\\\"done\\\",\\\"ip\\\":\\\"$(json_escape "$IP")\\\",\\\"mac\\\":\\\"$(json_escape "$MAC")\\\",\\\"iface\\\":\\\"eth0\\\"}"
`

// EthernetDisableScript brings eth0 down and removes the module.
const EthernetDisableScript = `#!/bin/sh
set -e
ifconfig eth0 down 2>/dev/null || true
kill "$(cat /var/run/udhcpc.eth0.pid 2>/dev/null)" 2>/dev/null || true
rmmod ax88179_178a 2>/dev/null || true
echo '{"ok":true,"msg":"Ethernet deaktiviert"}'
`

// EthernetStatusScript reads eth0 state.
const EthernetStatusScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\\r\\n' | sed 's/\\\\/\\\\\\\\/g;s/\"/\\\\\"/g'; }
ETH0_EXISTS=false
ETH0_UP=false
IP=""
MAC=""
LINK=""
AUTOSTART=false
[ -f /mnt/sd/eth-autostart ] && AUTOSTART=true
if [ -e /sys/class/net/eth0 ]; then
  ETH0_EXISTS=true
  ST=$(cat /sys/class/net/eth0/operstate 2>/dev/null || echo unknown)
  [ "$ST" = "up" ] && ETH0_UP=true
  MAC=$(ifconfig eth0 2>/dev/null | awk '/HWaddr/{print $5; exit}' || ifconfig eth0 2>/dev/null | awk '/ether/{print $2; exit}')
  IP=$(ifconfig eth0 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
  [ -z "$IP" ] && IP=$(ifconfig eth0 2>/dev/null | awk '/inet /{print $2}')
  LINK=$(cat /sys/class/net/eth0/carrier 2>/dev/null || echo 0)
fi
DRV_LOADED=false
lsmod 2>/dev/null | grep -q 'ax88179_178a' && DRV_LOADED=true
USB_POWER=$(cat /sys/bus/usb/devices/2-1/power/control 2>/dev/null || echo unknown)
echo "{\\\"ok\\\":true,\\\"exists\\\":$ETH0_EXISTS,\\\"up\\\":$ETH0_UP,\\\"ip\\\":\\\"$(json_escape "$IP")\\\",\\\"mac\\\":\\\"$(json_escape "$MAC")\\\",\\\"link\\\":$LINK,\\\"driver_loaded\\\":$DRV_LOADED,\\\"usb_power\\\":\\\"$(json_escape "$USB_POWER")\\\",\\\"autostart\\\":$AUTOSTART}"
`

// EthernetAutostartDeploy creates the sentinel file for boot-time Ethernet startup.
const EthernetAutostartDeploy = `#!/bin/sh
set -e
touch /mnt/sd/eth-autostart
sync
echo '{"ok":true,"msg":"Ethernet-Autostart aktiviert (/mnt/sd/eth-autostart)"}'
`

// EthernetAutostartRemove removes the sentinel file.
const EthernetAutostartRemove = `#!/bin/sh
set -e
rm -f /mnt/sd/eth-autostart
sync
echo '{"ok":true,"msg":"Ethernet-Autostart deaktiviert"}'
`

// TailscaleStatus ruft Status ab (JSON-Zeile).
const TailscaleStatus = `TS=/mnt/sd/tailscale
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
if [ ! -x "$TS" ]; then echo '{"ok":true,"running":false,"installed":false,"joined":false}'; exit 0; fi
if ! pgrep tailscaled >/dev/null 2>&1; then echo '{"ok":true,"running":false,"installed":true,"joined":false}'; exit 0; fi
IP=$($TS --socket=/var/run/tailscale/tailscaled.sock ip 2>/dev/null | head -1)
STATUS=$($TS --socket=/var/run/tailscale/tailscaled.sock status --json 2>/dev/null || true)
PREFS=$($TS --socket=/var/run/tailscale/tailscaled.sock debug prefs 2>/dev/null || true)
JOINED=false
EXITNODE=false
BACKEND=""
HOSTNAME=""
ROUTES=""
if [ -n "$STATUS" ]; then
  BACKEND=$(printf '%s' "$STATUS" | sed -n 's/.*"BackendState"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
  HOSTNAME=$(printf '%s' "$STATUS" | sed -n 's/.*"HostName"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
  printf '%s' "$STATUS" | grep -q '"HaveNodeKey"[[:space:]]*:[[:space:]]*true' && JOINED=true
  ROUTES=$(printf '%s' "$STATUS" | awk '
    /"PrimaryRoutes"[[:space:]]*:/ {inr=1; next}
    inr && /\]/ {inr=0}
    inr {gsub(/[",]/,""); gsub(/^[[:space:]]+|[[:space:]]+$/,""); if ($0!="" && $0!="[") { printf "%s%s", sep, $0; sep="," }}
  ')
fi
if [ -n "$PREFS" ]; then
  printf '%s' "$PREFS" | grep -q '"0.0.0.0/0"' && printf '%s' "$PREFS" | grep -q '"::/0"' && EXITNODE=true
  PREF_ROUTES=$(printf '%s' "$PREFS" | awk '
    /"AdvertiseRoutes"[[:space:]]*:/ {inr=1; next}
    inr && /\]/ {inr=0}
    inr {
      gsub(/[",]/,""); gsub(/^[[:space:]]+|[[:space:]]+$/,"")
      if ($0 != "" && $0 != "[" && $0 != "0.0.0.0/0" && $0 != "::/0") { printf "%s%s", sep, $0; sep="," }
    }
  ')
  [ -n "$PREF_ROUTES" ] && ROUTES="$PREF_ROUTES"
fi
echo "{\"ok\":true,\"running\":true,\"installed\":true,\"joined\":${JOINED},\"tailscale_ip\":\"$(json_escape "$IP")\",\"backend_state\":\"$(json_escape "$BACKEND")\",\"hostname\":\"$(json_escape "$HOSTNAME")\",\"advertise_exit_node\":${EXITNODE},\"advertise_routes\":\"$(json_escape "$ROUTES")\"}"
`

// shQuoteSingle embeds s in POSIX single quotes for remote sh (no base64/openssl on camera needed).
func shQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// APApplyScript updates only /var/run/hostapd.conf at runtime; boot restores
// values through the vendor/app path unless persistence is handled separately.
func APApplyScript(ssid, pwd string) string {
	qS := shQuoteSingle(ssid)
	qP := shQuoteSingle(pwd)
	return fmt.Sprintf(`#!/bin/sh
set -e
SSID=%s
PWD=%s
`, qS, qP) + `
persist_usr1_wifi() {
  [ -e /dev/mtd7 ] || return 0
  command -v flashcp >/dev/null 2>&1 || { echo '{"ok":false,"error":"flashcp missing for usr1"}'; exit 1; }
  [ ${#SSID} -le 31 ] || { echo '{"ok":false,"error":"SSID too long for usr1"}'; exit 1; }
  [ -z "$PWD" ] || [ ${#PWD} -le 31 ] || { echo '{"ok":false,"error":"password too long for usr1"}'; exit 1; }

  mkdir -p /mnt/sd/.tmp
  TS=$(date +%Y%m%d-%H%M%S 2>/dev/null || echo now)
  BACK=/mnt/sd/usr1-backup-$TS.bin
  TMP=/mnt/sd/.tmp/usr1-patched-$TS.bin
  FIELD=/mnt/sd/.tmp/usr1-field-$$
  dd if=/dev/mtd7 of="$BACK" bs=262144 count=1 2>/dev/null || { echo '{"ok":false,"error":"usr1 backup fehlgeschlagen"}'; exit 1; }
  cp "$BACK" "$TMP" || { echo '{"ok":false,"error":"usr1 temp copy fehlgeschlagen"}'; exit 1; }

  write_field() {
    VALUE=$1
    OFFSET=$2
    dd if=/dev/zero of="$FIELD" bs=32 count=1 2>/dev/null
    printf '%s' "$VALUE" | dd of="$FIELD" bs=1 conv=notrunc 2>/dev/null
    dd if="$FIELD" of="$TMP" bs=1 seek="$OFFSET" conv=notrunc 2>/dev/null
  }
  write_field "$SSID" 10488
  write_field "$PWD" 10520
  flashcp "$TMP" /dev/mtd7 >/dev/null 2>&1 || { echo '{"ok":false,"error":"usr1 flashcp fehlgeschlagen","backup":"'"$BACK"'"}'; exit 1; }
  rm -f "$TMP" "$FIELD"
}

if [ -n "$PWD" ] && [ ${#PWD} -ge 8 ]; then
cat > /var/run/hostapd.conf << EOF
interface=wlan0
driver=nl80211
ssid=${SSID}
wpa=2
wpa_pairwise=CCMP
wpa_key_mgmt=WPA-PSK
wpa_passphrase=${PWD}
country_code=US
hw_mode=g
channel=6
ieee80211n=1
ctrl_interface=/var/run/hostapd
EOF
else
cat > /var/run/hostapd.conf << EOF
interface=wlan0
driver=nl80211
ssid=${SSID}
hw_mode=g
channel=6
ctrl_interface=/var/run/hostapd
EOF
fi
persist_usr1_wifi
sync
killall hostapd 2>/dev/null || true
killall udhcpd 2>/dev/null || true
sleep 1
hostapd -B /var/run/hostapd.conf
sleep 1
udhcpd -S /etc/udhcpdw.conf 2>/dev/null || true
if pgrep hostapd >/dev/null 2>&1; then echo '{"ok":true,"msg":"AP neu gestartet und usr1 persistiert (/dev/mtd7)"}'; else echo '{"ok":false,"error":"hostapd konnte nicht starten"}'; exit 1; fi
`
}

// FlespiInstallScript schreibt /mnt/sd/flespi.conf und /mnt/sd/flespi-ping.sh (curl-GW-Check).
// token leer: nur Platzhalter-Zeile; sonst eine Zeile FLESPI_TOKEN=… (Wert shell-sicher gequotet).
func FlespiInstallScript(token string) string {
	token = strings.TrimSpace(token)
	var confBlock string
	if token == "" {
		confBlock = `printf '%s\n' '# FLESPI_TOKEN=… eintragen oder vom Installer deployen' > /mnt/sd/flespi.conf
printf '%s\n' 'FLESPI_TOKEN=' >> /mnt/sd/flespi.conf
`
	} else {
		line := "FLESPI_TOKEN=" + token
		confBlock = fmt.Sprintf("printf '%%s\\n' %s > /mnt/sd/flespi.conf\n", shQuoteSingle(line))
	}
	return `#!/bin/sh
set -e
` + confBlock + `cat > /mnt/sd/flespi-ping.sh << 'FLESPI_PING_EOF'
#!/bin/sh
set -e
CONF=/mnt/sd/flespi.conf
[ -r "$CONF" ] && . "$CONF"
[ -n "$FLESPI_TOKEN" ] || { echo '{"ok":false,"error":"FLESPI_TOKEN fehlt in /mnt/sd/flespi.conf"}'; exit 1; }
OUT=$(curl -sS --connect-timeout 8 -H "Authorization: FlespiToken $FLESPI_TOKEN" "https://flespi.io/gw/devices/all?fields=id,name&limit=3" 2>&1) || true
echo "$OUT" | head -c 1200
echo
FLESPI_PING_EOF
chmod +x /mnt/sd/flespi-ping.sh
sync
echo '{"ok":true,"msg":"flespi scripts unter /mnt/sd/flespi-ping.sh und /mnt/sd/flespi.conf"}'
`
}

// TailscaleAutostartDeploy sets the sentinel file /mnt/sd/tailscale-autostart.
// The main_app wrapper reads it at boot and starts tailscaled (persistent via SD + ubifs wrapper).
// Note: /etc/init.d/ lives on RAMDISK (NVT_ROOTFS_TYPE_RAMDISK, no overlay) — not persistent.
// Autostart is therefore handled via the main_app wrapper in /usr/bin/ (ubifs, persistent).
const TailscaleAutostartDeploy = `#!/bin/sh
set -e
WRAP=/usr/bin/main_app
if ! head -1 "$WRAP" 2>/dev/null | grep -q '^#!/bin/sh'; then
  echo '{"ok":false,"error":"main_app-Wrapper nicht installiert — zuerst WiFi-Client-Wrapper installieren"}'; exit 0
fi
[ -x /mnt/sd/tailscaled ] || { echo '{"ok":false,"error":"tailscaled fehlt auf SD — zuerst Tailscale-Binaries laden"}'; exit 0; }
[ -f /mnt/sd/tailscale-state ] || { echo '{"ok":false,"error":"tailscale-state fehlt — zuerst tailscale up ausfuehren"}'; exit 0; }
touch /mnt/sd/tailscale-autostart
sync
echo '{"ok":true,"msg":"Tailscale-Autostart aktiviert (/mnt/sd/tailscale-autostart). Wirksam nach nächstem Reboot via main_app-Wrapper."}'
`

// WifiWrapperInstallScript installiert den main_app-Wrapper in /usr/bin/ (ubifs, persistent).
// Der Wrapper patcht /usr/share/wifiscripts/up.sh beim Boot mit dem SD-Client-Mode-Override.
const WifiWrapperInstallScript = `#!/bin/sh
set -e
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }

[ -x /mnt/sd/wpalib/wpa_supplicant ] || { echo '{"ok":false,"error":"wpalib fehlt — zuerst wpalib hochladen (/mnt/sd/wpalib/wpa_supplicant)"}'; exit 0; }

REAL=/usr/bin/main_app.real
WRAP=/usr/bin/main_app

if [ -f "$REAL" ] && head -1 "$REAL" | grep -q '^#!/bin/sh'; then
  echo '{"ok":false,"error":"main_app.real ist bereits ein Shell-Script — Wrapper schon installiert oder inkonsistenter Zustand"}'; exit 0
fi

if [ ! -f "$REAL" ]; then
  mv "$WRAP" "$REAL" || { echo '{"ok":false,"error":"mv main_app -> main_app.real fehlgeschlagen"}'; exit 1; }
fi

cat > "$WRAP" << 'WRAPPER_EOF'
#!/bin/sh
# Tailscale-Autostart: Sentinel-Datei auf SD steuert ob tailscaled beim Boot startet
if [ -f /mnt/sd/tailscale-autostart ] && [ -x /mnt/sd/tailscaled ] && [ -f /mnt/sd/tailscale-state ]; then
    mkdir -p /var/run/tailscale /dev/net
    mknod /dev/net/tun c 10 200 2>/dev/null || true
    /mnt/sd/tailscaled --state=/mnt/sd/tailscale-state --tun=userspace-networking \
        --socket=/var/run/tailscale/tailscaled.sock &
fi
# Ethernet-Autostart: AX88179 USB-ETH beim Boot aktivieren
if [ -f /mnt/sd/eth-autostart ]; then
    lsmod | grep -q ax88179_178a || modprobe ax88179_178a 2>/dev/null || true
    sleep 2
    if [ -e /sys/class/net/eth0 ]; then
        ifconfig eth0 up 2>/dev/null
        udhcpc -i eth0 -n -q -t 3 -T 2 >/dev/null 2>&1 || true
    fi
fi
# Flespi-Daemon-Autostart: Sentinel-Datei auf SD steuert Telemetrie-Start beim Boot
if [ -f /mnt/sd/flespi-autostart ] && [ -x /mnt/sd/flespi-daemon.sh ]; then
    if ! ps 2>/dev/null | awk '$0 ~ /[s]h[[:space:]]+\/mnt\/sd\/flespi-daemon\.sh/ {found=1} END{exit found?0:1}'; then
        nohup sh /mnt/sd/flespi-daemon.sh >/dev/null 2>&1 &
        echo $! > /mnt/sd/flespi-daemon.pid 2>/dev/null || true
    fi
fi
# WiFi Client-Mode Override: up.sh patchen
UPSH=/usr/share/wifiscripts/up.sh
if ! grep -q "SD-Client-Mode" "$UPSH" 2>/dev/null; then
  LINENUM=$(grep -n 'if \[ "\$1" == "ap" \]' "$UPSH" 2>/dev/null | head -1 | cut -d: -f1)
  if [ -n "$LINENUM" ]; then
    head -n $((LINENUM - 1)) "$UPSH" > /var/run/up_patched.sh 2>/dev/null
    cat >> /var/run/up_patched.sh << 'INSERT'
# SD-Client-Mode Override
if [ "$1" = "ap" ] && [ -f /mnt/sd/wpa_supplicant.conf ]; then
    echo "[up.sh] SD wpa_supplicant.conf -> STA-Mode"
    WIFI_LOG=/mnt/sd/wifi-client-health.log
    wifi_log() { printf '[%s] %s\n' "$(date '+%H:%M:%S' 2>/dev/null || echo '?')" "$*" >> "$WIFI_LOG" 2>/dev/null || true; }
    wifi_ip() {
        IP_NOW=$(ifconfig wlan0 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
        [ -z "$IP_NOW" ] && IP_NOW=$(ifconfig wlan0 2>/dev/null | awk '/inet /{print $2}')
        printf '%s' "$IP_NOW"
    }
    wifi_gw() {
        GW_NOW=$(ip route 2>/dev/null | awk '/^default /{print $3; exit}')
        [ -z "$GW_NOW" ] && GW_NOW=$(route -n 2>/dev/null | awk '$1=="0.0.0.0"{print $2; exit}')
        printf '%s' "$GW_NOW"
    }
    wifi_ping_ok() {
        ping -c 1 -W 2 8.8.8.8 >/dev/null 2>&1 || ping -c 1 -W 2 1.1.1.1 >/dev/null 2>&1
    }
    wifi_dhcp_once() {
        kill "$(cat /var/run/udhcpc.pid 2>/dev/null)" 2>/dev/null || true
        rm -f /var/run/udhcpc.pid 2>/dev/null || true
        udhcpc -i wlan0 -p /var/run/udhcpc.pid -T 5 -t 3 -n -q >/dev/null 2>&1
    }
    wifi_wpa_status() {
        if [ -x /mnt/sd/wpalib/wpa_cli ]; then
            /mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib \
                /mnt/sd/wpalib/wpa_cli -i wlan0 status 2>/dev/null
        else
            printf ''
        fi
    }
    wifi_wait_assoc() {
        N=0
        while [ "$N" -lt 15 ]; do
            N=$((N + 1))
            STATUS=$(wifi_wpa_status)
            STATE=$(printf '%s\n' "$STATUS" | awk -F= '$1=="wpa_state"{print $2; exit}')
            IP_FROM_WPA=$(printf '%s\n' "$STATUS" | awk -F= '$1=="ip_address"{print $2; exit}')
            wifi_log "assoc attempt=$N state=${STATE:-unknown} ip=${IP_FROM_WPA:-none}"
            [ "$STATE" = "COMPLETED" ] && return 0
            sleep 2
        done
        return 1
    }
    wifi_client_fallback_ap() {
        TS=$(date +%Y%m%d-%H%M%S 2>/dev/null || echo now)
        wifi_log "fallback-ap start ts=$TS"
        if [ -f /mnt/sd/wpa_supplicant.conf ]; then
            mv /mnt/sd/wpa_supplicant.conf "/mnt/sd/wpa_supplicant.conf.failed-$TS" 2>/dev/null || rm -f /mnt/sd/wpa_supplicant.conf
        fi
        if [ -f /mnt/sd/network.conf ]; then
            mv /mnt/sd/network.conf "/mnt/sd/network.conf.failed-$TS" 2>/dev/null || rm -f /mnt/sd/network.conf
        fi
        kill "$(cat /var/run/udhcpc.pid 2>/dev/null)" 2>/dev/null || true
        kill "$(cat /var/run/wpa_supplicant.pid 2>/dev/null)" 2>/dev/null || true
        killall wpa_supplicant 2>/dev/null || true
        sync
        wifi_log "fallback-ap reboot"
        (sleep 2 && /sbin/reboot) >/dev/null 2>&1 &
        exit 0
    }
    wifi_health_loop() {
        wifi_log "health-loop start"
        N=0
        while [ "$N" -lt 6 ]; do
            N=$((N + 1))
            IP_NOW=$(wifi_ip)
            GW_NOW=$(wifi_gw)
            if [ -z "$IP_NOW" ] || [ "$IP_NOW" = "0.0.0.0" ]; then
                wifi_log "no-ip attempt=$N -> dhcp renew"
                wifi_dhcp_once || true
            elif [ -z "$GW_NOW" ]; then
                wifi_log "ip=$IP_NOW no-default-route attempt=$N"
                if [ -r /mnt/sd/network.conf ]; then
                    . /mnt/sd/network.conf
                    [ -n "$GW" ] && route add default gw "$GW" 2>/dev/null || true
                else
                    wifi_dhcp_once || true
                fi
            elif wifi_ping_ok; then
                wifi_log "ok ip=$IP_NOW gw=$GW_NOW"
                exit 0
            else
                wifi_log "ip=$IP_NOW gw=$GW_NOW no-internet attempt=$N -> dhcp renew"
                wifi_dhcp_once || true
            fi
            sleep 8
        done
        wifi_log "failed after retries ip=$(wifi_ip) gw=$(wifi_gw)"
        wifi_client_fallback_ap
    }
    cp /mnt/sd/wpa_supplicant.conf /var/run/wpa_supplicant.conf
    killall wpa_supplicant 2>/dev/null || true
    /mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib \
        /mnt/sd/wpalib/wpa_supplicant -B -i wlan0 -c /var/run/wpa_supplicant.conf \
        -D nl80211 -P /var/run/wpa_supplicant.pid
    if ! wifi_wait_assoc; then
        wifi_log "association failed -> fallback"
        wifi_client_fallback_ap
    fi
    if [ -f /mnt/sd/network.conf ]; then
        . /mnt/sd/network.conf
        ifconfig wlan0 "$IP" netmask "${MASK:-255.255.255.0}"
        [ -n "$GW" ] && route add default gw "$GW" 2>/dev/null || true
        [ -n "$DNS" ] && echo "nameserver $DNS" > /etc/resolv.conf
    else
        for DHCP_TRY in 1 2 3 4; do
            wifi_log "initial dhcp try=$DHCP_TRY"
            wifi_dhcp_once && break
            sleep 3
        done
    fi
    wifi_health_loop &
    exit 0
fi
INSERT
    tail -n +$LINENUM "$UPSH" >> /var/run/up_patched.sh
    chmod +x /var/run/up_patched.sh
    cp /var/run/up_patched.sh "$UPSH"
  fi
fi
exec /usr/bin/main_app.real "$@"
WRAPPER_EOF
chmod +x "$WRAP"
sync
SIZE=$(wc -c < "$WRAP" 2>/dev/null)
echo "{\"ok\":true,\"msg\":\"Wrapper installiert (${SIZE} Bytes). Wirksam nach nächstem Reboot.\",\"real\":\"$REAL\",\"wrapper\":\"$WRAP\"}"
`

// WifiWrapperRemoveScript stellt das originale main_app-Binary wieder her.
const WifiWrapperRemoveScript = `#!/bin/sh
set -e
REAL=/usr/bin/main_app.real
WRAP=/usr/bin/main_app
if [ ! -f "$REAL" ]; then
  echo '{"ok":false,"error":"main_app.real nicht gefunden — Wrapper war nicht installiert"}'; exit 0
fi
if head -1 "$REAL" 2>/dev/null | grep -q '^#!/bin/sh'; then
  echo '{"ok":false,"error":"main_app.real ist ein Shell-Script — inkonsistenter Zustand"}'; exit 0
fi
cp "$REAL" "$WRAP"
chmod +x "$WRAP"
rm -f "$REAL"
sync
echo '{"ok":true,"msg":"Wrapper entfernt — originales main_app wiederhergestellt. Wirksam nach nächstem Reboot."}'
`

// WifiWrapperStatusScript checks whether the wrapper is installed.
const WifiWrapperStatusScript = `#!/bin/sh
REAL=/usr/bin/main_app.real
WRAP=/usr/bin/main_app
REAL_EXISTS=false
WRAP_IS_SCRIPT=false
PATCH_IN_UPSH=false
[ -f "$REAL" ] && REAL_EXISTS=true
head -1 "$WRAP" 2>/dev/null | grep -q '^#!/bin/sh' && WRAP_IS_SCRIPT=true
grep -q "SD-Client-Mode" /usr/share/wifiscripts/up.sh 2>/dev/null && PATCH_IN_UPSH=true
printf '{"ok":true,"wrapper_installed":%s,"upsh_patched":%s}\n' "$WRAP_IS_SCRIPT" "$PATCH_IN_UPSH"
`

// WifiStatusScript returns mode, ssid, ip, wrapper_installed, wpa_conf_on_sd.
const WifiStatusScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
proc_running() { pgrep "$1" >/dev/null 2>&1 || ps 2>/dev/null | grep -q "[${1%${1#?}}]${1#?}"; }
MODE=unknown
SSID=""
IP=$(ifconfig wlan0 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
[ -z "$IP" ] && IP=$(ifconfig wlan0 2>/dev/null | awk '/inet /{print $2}')

# Client IP wins over stale AP artifacts. main_app may leave hostapd/AP config
# around, but a non-AP address plus wpa_supplicant means the camera is client.
case "${IP:-}" in
  192.168.0.1|192.168.0.*|"") : ;;
  *)
    if proc_running wpa_supplicant || [ -f /mnt/sd/wpa_supplicant.conf ] || [ -f /var/run/wpa_supplicant.conf ]; then
      MODE=client
    fi
    ;;
esac
if [ "$MODE" = "unknown" ] && proc_running wpa_supplicant; then
  MODE=client
fi
if [ "$MODE" = "client" ]; then
  if [ -x /mnt/sd/wpalib/wpa_cli ]; then
    SSID=$(/mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib /mnt/sd/wpalib/wpa_cli -i wlan0 status 2>/dev/null | awk -F= '$1=="ssid"{print $2; exit}')
  fi
  [ -z "$SSID" ] && SSID=$(grep 'ssid=' /mnt/sd/wpa_supplicant.conf /var/run/wpa_supplicant.conf 2>/dev/null | grep -v '#' | head -1 | tr -d '"' | cut -d= -f2-)
elif proc_running hostapd || [ "${IP:-}" = "192.168.0.1" ]; then
  MODE=ap
  SSID=$(grep '^ssid=' /var/run/hostapd.conf 2>/dev/null | head -1 | cut -d= -f2-)
  [ -z "$SSID" ] && SSID=$(grep '^ssid=' /etc/wifiap_wpa2.conf 2>/dev/null | head -1 | cut -d= -f2-)
fi
# Fallback: wpa_supplicant.conf auf SD vorhanden + IP nicht im 192.168.0.x-Bereich → client
if [ "$MODE" = "unknown" ] && [ -f /mnt/sd/wpa_supplicant.conf ]; then
  case "${IP:-}" in 192.168.0.*|"") : ;; *) MODE=client ;; esac
  if [ "$MODE" = "client" ]; then
    if [ -x /mnt/sd/wpalib/wpa_cli ]; then
      SSID=$(/mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib /mnt/sd/wpalib/wpa_cli -i wlan0 status 2>/dev/null | awk -F= '$1=="ssid"{print $2; exit}')
    fi
    [ -z "$SSID" ] && SSID=$(grep 'ssid=' /mnt/sd/wpa_supplicant.conf 2>/dev/null | grep -v '#' | head -1 | tr -d '"' | cut -d= -f2-)
  fi
fi
# Fallback: IP ist 192.168.0.1 → ap
if [ "$MODE" = "unknown" ] && [ "${IP:-}" = "192.168.0.1" ]; then
  MODE=ap
  SSID=$(grep '^ssid=' /var/run/hostapd.conf /etc/wifiap_wpa2.conf 2>/dev/null | head -1 | cut -d= -f2-)
fi
WRAPPER_INSTALLED=false
head -1 /usr/bin/main_app 2>/dev/null | grep -q '^#!/bin/sh' && WRAPPER_INSTALLED=true
WPA_CONF_ON_SD=false
[ -f /mnt/sd/wpa_supplicant.conf ] && WPA_CONF_ON_SD=true
WPALIB_ON_SD=false
[ -x /mnt/sd/wpalib/wpa_supplicant ] && WPALIB_ON_SD=true
printf '{"ok":true,"mode":"%s","ssid":"%s","ip":"%s","wrapper_installed":%s,"wpa_conf_on_sd":%s,"wpalib_on_sd":%s}\n' \
  "$(json_escape "$MODE")" "$(json_escape "$SSID")" "$(json_escape "${IP:-?}")" \
  "$WRAPPER_INSTALLED" "$WPA_CONF_ON_SD" "$WPALIB_ON_SD"
`

// WifiHealthScript runs on the camera and checks real client connectivity.
const WifiHealthScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
proc_running() { pgrep "$1" >/dev/null 2>&1 || ps 2>/dev/null | grep -q "[${1%${1#?}}]${1#?}"; }
IFACE=wlan0
IP=$(ifconfig "$IFACE" 2>/dev/null | awk '/inet addr/{print $2}' | cut -d: -f2)
[ -z "$IP" ] && IP=$(ifconfig "$IFACE" 2>/dev/null | awk '/inet /{print $2}')
MODE=unknown
proc_running wpa_supplicant && MODE=client
proc_running hostapd && MODE=ap

GW=$(ip route 2>/dev/null | awk '/^default /{print $3; exit}')
[ -z "$GW" ] && GW=$(route -n 2>/dev/null | awk '$1=="0.0.0.0"{print $2; exit}')
DNS=$(grep '^nameserver ' /etc/resolv.conf /mnt/sd/network.conf 2>/dev/null | awk '{print $2}' | head -1)
HEALTH_LOG=""
[ -r /mnt/sd/wifi-client-health.log ] && HEALTH_LOG=$(tail -5 /mnt/sd/wifi-client-health.log 2>/dev/null | tr '\n' '|' | sed 's/"/\\"/g')

ping_one() {
  TARGET=$1
  LABEL=$2
  [ -n "$TARGET" ] || return 0
  OUT=$(ping -c 1 -W 2 "$TARGET" 2>&1)
  RC=$?
  OK=false
  [ "$RC" = "0" ] && OK=true
  MS=$(printf '%s' "$OUT" | sed -n 's/.*time[=<]\([0-9.]*\).*/\1/p' | head -1)
  printf '{"target":"%s","label":"%s","ok":%s,"ms":"%s","raw":"%s"}' \
    "$(json_escape "$TARGET")" "$(json_escape "$LABEL")" "$OK" "$(json_escape "$MS")" "$(json_escape "$(printf '%s' "$OUT" | tail -2)")"
  [ "$OK" = "true" ] && return 0
  return 1
}

RESULTS=""
SEP=""
PUBLIC_OK=false
GATEWAY_OK=false

for SPEC in "8.8.8.8:google_dns" "1.1.1.1:cloudflare_dns"; do
  TARGET=${SPEC%%:*}
  LABEL=${SPEC#*:}
  RES=$(ping_one "$TARGET" "$LABEL")
  RC=$?
  RESULTS="${RESULTS}${SEP}${RES}"
  SEP=","
  [ "$RC" = "0" ] && PUBLIC_OK=true
done

if [ -n "$GW" ]; then
  RES=$(ping_one "$GW" "gateway")
  RC=$?
  RESULTS="${RESULTS}${SEP}${RES}"
  SEP=","
  [ "$RC" = "0" ] && GATEWAY_OK=true
fi

if [ -n "$DNS" ] && [ "$DNS" != "$GW" ] && [ "$DNS" != "8.8.8.8" ] && [ "$DNS" != "1.1.1.1" ]; then
  RES=$(ping_one "$DNS" "configured_dns")
  RESULTS="${RESULTS}${SEP}${RES}"
fi

printf '{"ok":true,"mode":"%s","iface":"%s","ip":"%s","gateway":"%s","dns":"%s","internet_ok":%s,"gateway_ok":%s,"health_log":"%s","results":[%s]}\n' \
  "$(json_escape "$MODE")" "$(json_escape "$IFACE")" "$(json_escape "${IP:-}")" \
  "$(json_escape "$GW")" "$(json_escape "$DNS")" "$PUBLIC_OK" "$GATEWAY_OK" "$(json_escape "$HEALTH_LOG")" "$RESULTS"
`

// WifiScanScript reads available WLANs, preferring the RTL8851BU survey results.
// Aktive Scans sind gedrosselt, weil der Treiber bei BusyTraffic dmesg spammt.
const WifiScanScript = `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
IFACE=wlan0
for c in wlan0 wlan1; do [ -d "/sys/class/net/$c" ] && IFACE=$c && break; done
CACHE=/var/run/x800-wifi-scan-cache.json
STAMP=/var/run/x800-wifi-scan-last
NOW=$(date +%s 2>/dev/null || echo 0)
LAST=0
[ -r "$STAMP" ] && LAST=$(cat "$STAMP" 2>/dev/null || echo 0)
case "$LAST" in ""|*[!0-9]*) LAST=0 ;; esac
AGE=$(( NOW - LAST ))
if [ "$AGE" -ge 0 ] && [ "$AGE" -lt 45 ] && [ -s "$CACHE" ]; then
  cat "$CACHE"
  exit 0
fi
OUT=/tmp/x800-wifi-scan-$$.json
exec 3>&1
exec > "$OUT"
finish_scan() {
  exec 1>&3
  cat "$OUT" 2>/dev/null || printf '[]'
  cp "$OUT" "$CACHE" 2>/dev/null || true
  echo "$NOW" > "$STAMP" 2>/dev/null || true
  rm -f "$OUT" 2>/dev/null || true
  exit 0
}
wpa_completed() {
  if [ -x /mnt/sd/wpalib/wpa_cli ]; then
    /mnt/sd/wpalib/ld-musl-aarch64.so.1 --library-path /mnt/sd/wpalib \
      /mnt/sd/wpalib/wpa_cli -i "$IFACE" status 2>/dev/null | grep -q '^wpa_state=COMPLETED'
  else
    return 1
  fi
}
SURVEY="/proc/net/rtl8851bu/${IFACE}/survey_info"
if [ -r "$SURVEY" ]; then
  printf '['
  FIRST=1
  while IFS= read -r line; do
    case "$line" in index*|"") continue ;; esac
    BSSID=$(printf '%s\n' "$line" | awk '{print $2}')
    [ -n "$BSSID" ] || continue
    BAND=$(printf '%s\n' "$line" | awk '{print $3}')
    CH=$(printf '%s\n' "$line" | awk '{print $4}')
    RSSI=$(printf '%s\n' "$line" | awk '{print $5}')
    FLAGS=$(printf '%s\n' "$line" | awk '{print $11}')
    SSID=$(printf '%s\n' "$line" | awk '{for(i=12;i<=NF;i++) printf "%s%s",$i,(i<NF?" ":""); print ""}')
    OPEN=false
    printf '%s' "$FLAGS" | grep -qE 'WPA|PSK|WEP' || OPEN=true
    [ "$FIRST" = "1" ] || printf ','
    FIRST=0
    printf '{"ssid":"%s","bssid":"%s","band":"%s","freq":"ch%s","signal":"%s","rssi":"%s","open":%s}' \
      "$(json_escape "$SSID")" "$(json_escape "$BSSID")" "$(json_escape "$BAND")" \
      "$(json_escape "$CH")" "$(json_escape "$RSSI")" "$(json_escape "$RSSI")" "$OPEN"
  done < "$SURVEY"
  printf ']'
  finish_scan
fi

if wpa_completed; then
  printf '[]'
  finish_scan
fi

# iw Fallback
if command -v iw >/dev/null 2>&1; then
  RAW=$(iw dev "$IFACE" scan 2>/dev/null)
  if [ -n "$RAW" ]; then
    printf '['
    FIRST=1
    SSID="" SIGNAL="" FREQ=""
    emit_iw() {
      [ -n "$SSID" ] || return 0
      [ "$FIRST" = "1" ] || printf ','
      FIRST=0
      printf '{"ssid":"%s","signal":"%s","freq":"%s"}' \
        "$(json_escape "$SSID")" "$(json_escape "$SIGNAL")" "$(json_escape "$FREQ")"
    }
    while IFS= read -r line; do
      case "$line" in
        *"SSID: "*)
          SSID=${line#*SSID: } ;;
        *"signal: "*)
          SIGNAL=$(echo "$line" | grep -oE '\-[0-9]+\.[0-9]+|\-[0-9]+' | head -1) ;;
        *"freq: "*)
          FREQ=$(echo "$line" | grep -oE '[0-9]{4,5}' | head -1) ;;
        *"BSS "*)
          emit_iw
          SSID="" SIGNAL="" FREQ="" ;;
      esac
    done << SCANEOF
$RAW
SCANEOF
    emit_iw
    printf ']'
    finish_scan
  fi
fi
# iwlist Fallback
if command -v iwlist >/dev/null 2>&1; then
  RAW=$(iwlist "$IFACE" scan 2>/dev/null)
  if [ -n "$RAW" ]; then
    printf '['
    FIRST=1
    printf '%s' "$RAW" | grep 'ESSID:' | sed 's/.*ESSID:"//' | sed 's/"//' | while IFS= read -r s; do
      [ "$FIRST" = "1" ] && FIRST=0 || printf ','
      printf '{"ssid":"%s"}' "$(json_escape "$s")"
    done
    printf ']'
    finish_scan
  fi
fi
printf '[]'
finish_scan
`

// WifiConnectScript writes wpa_supplicant.conf to SD and reboots.
// Wrapper must already be installed (done beforehand via WifiWrapperInstallScript).
func WifiConnectScript(ssid, pwd, staticIP, mask, gw, dns string) string {
	qSSID := shQuoteSingle(ssid)
	qPWD := shQuoteSingle(pwd)
	qIP := shQuoteSingle(staticIP)
	qMask := shQuoteSingle(mask)
	qGW := shQuoteSingle(gw)
	qDNS := shQuoteSingle(dns)

	return fmt.Sprintf(`#!/bin/sh
set -e
SSID=%s
PWD=%s
STATICIP=%s
MASK=%s
GW=%s
DNS=%s
`, qSSID, qPWD, qIP, qMask, qGW, qDNS) + `
[ -n "$SSID" ] || { echo '{"ok":false,"error":"SSID fehlt — kein Connect, kein Reboot"}'; exit 1; }
[ -x /mnt/sd/wpalib/wpa_supplicant ] || { echo '{"ok":false,"error":"wpalib fehlt auf SD (/mnt/sd/wpalib/)"}'; exit 0; }
head -1 /usr/bin/main_app 2>/dev/null | grep -q '^#!/bin/sh' || { echo '{"ok":false,"error":"main_app-Wrapper nicht installiert — zuerst Wrapper installieren"}'; exit 0; }

if [ -z "$PWD" ]; then
cat > /mnt/sd/wpa_supplicant.conf << EOF
ctrl_interface=/var/run/wpa_supplicant
network={
    ssid="$SSID"
    key_mgmt=NONE
}
EOF
else
cat > /mnt/sd/wpa_supplicant.conf << EOF
ctrl_interface=/var/run/wpa_supplicant
update_config=1
network={
    ssid="$SSID"
    proto=RSN
    key_mgmt=WPA-PSK
    pairwise=CCMP TKIP
    group=CCMP TKIP
    psk="$PWD"
}
EOF
fi

if printf '%s' "$STATICIP" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
  printf 'IP=%s\nMASK=%s\nGW=%s\nDNS=%s\n' "$STATICIP" "${MASK:-255.255.255.0}" "$GW" "$DNS" > /mnt/sd/network.conf
else
  rm -f /mnt/sd/network.conf
fi

sync
echo '{"ok":true,"msg":"wpa_supplicant.conf geschrieben. Kamera startet neu und verbindet sich mit dem WLAN...","reboot":true}'
(sleep 2 && /sbin/reboot) &
`
}

// WifiDisconnectScript removes wpa_supplicant.conf and reboots back into AP mode.
const WifiDisconnectScript = `#!/bin/sh
rm -f /mnt/sd/wpa_supplicant.conf /mnt/sd/network.conf
sync
echo '{"ok":true,"msg":"wpa_supplicant.conf entfernt. Kamera startet neu in AP-Mode...","reboot":true}'
(sleep 2 && /sbin/reboot) &
`

// TailscaleAutostartRemove entfernt die Sentinel-Datei.
const TailscaleAutostartRemove = `#!/bin/sh
rm -f /mnt/sd/tailscale-autostart
sync
echo '{"ok":true,"msg":"Tailscale-Autostart deaktiviert (/mnt/sd/tailscale-autostart entfernt)"}'
`

// TailscaleAutostartStatus checks whether the sentinel file and wrapper are present.
const TailscaleAutostartStatus = `#!/bin/sh
SENTINEL=/mnt/sd/tailscale-autostart
WRAPPER_OK=false
head -1 /usr/bin/main_app 2>/dev/null | grep -q '^#!/bin/sh' && WRAPPER_OK=true
if [ -f "$SENTINEL" ] && [ "$WRAPPER_OK" = "true" ]; then
  echo '{"ok":true,"installed":true,"wrapper_ok":true}'
elif [ -f "$SENTINEL" ]; then
  echo '{"ok":true,"installed":true,"wrapper_ok":false}'
else
  echo '{"ok":true,"installed":false,"wrapper_ok":'"$WRAPPER_OK"'}'
fi
`

// TailscaleUpOpts holds all optional parameters for tailscale up.
type TailscaleUpOpts struct {
	AuthKey      string
	Hostname     string // --hostname
	Routes       string // --advertise-routes (comma-separated CIDRs)
	ExitNode     bool   // --advertise-exit-node
	AcceptRoutes bool   // --accept-routes
}

// TailscaleUpScript: starts tailscaled if needed, then joins via tailscale up or
// applies only preferences via tailscale set for already-joined nodes.
func TailscaleUpScript(opts TailscaleUpOpts) string {
	qK := shQuoteSingle(opts.AuthKey)

	// Build optional flags as a shell argument string.
	var extraFlags strings.Builder
	if opts.Hostname != "" {
		extraFlags.WriteString(fmt.Sprintf(" --hostname=%s", shQuoteSingle(opts.Hostname)))
	}
	if opts.Routes != "" {
		extraFlags.WriteString(fmt.Sprintf(" --advertise-routes=%s", shQuoteSingle(opts.Routes)))
	}
	if opts.ExitNode {
		extraFlags.WriteString(" --advertise-exit-node")
	}
	if opts.AcceptRoutes {
		extraFlags.WriteString(" --accept-routes")
	}

	return fmt.Sprintf(`#!/bin/sh
set -e
json_escape() {
  printf '%%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'
}
KEY=%s
FLAGS=%s
[ -x /mnt/sd/tailscale ] || { echo '{"ok":false,"error":"tailscale binary fehlt"}'; exit 0; }
if ! pgrep tailscaled >/dev/null 2>&1; then
  mkdir -p /var/run/tailscale /dev/net
  mknod /dev/net/tun c 10 200 2>/dev/null || true
  /mnt/sd/tailscaled --state=/mnt/sd/tailscale-state --tun=userspace-networking --socket=/var/run/tailscale/tailscaled.sock &
  sleep 3
fi
JOINED=false
STATUS=$(/mnt/sd/tailscale --socket=/var/run/tailscale/tailscaled.sock status --json 2>/dev/null || true)
printf '%%s' "$STATUS" | grep -q '"HaveNodeKey"[[:space:]]*:[[:space:]]*true' && JOINED=true
set +e
if [ "$JOINED" = true ]; then
  ACTION=set
  if [ -n "$FLAGS" ]; then
    OUT=$(eval "/mnt/sd/tailscale --socket=/var/run/tailscale/tailscaled.sock set $FLAGS" 2>&1)
    RC=$?
  else
    OUT="bereits gejoint; keine neuen Preferences"
    RC=0
  fi
elif [ -n "$KEY" ]; then
  OUT=$(eval "/mnt/sd/tailscale --socket=/var/run/tailscale/tailscaled.sock up --authkey=\"\$KEY\" $FLAGS" 2>&1)
  ACTION=up
  RC=$?
else
  OUT="authkey fehlt und Node ist noch nicht gejoint"
  ACTION=up
  RC=2
fi
set -e
ESC=$(json_escape "$OUT" | head -c 800)
IP=$(/mnt/sd/tailscale --socket=/var/run/tailscale/tailscaled.sock ip 2>/dev/null | head -1)
if [ "$RC" -eq 0 ]; then echo "{\"ok\":true,\"msg\":\"${ACTION}\",\"joined\":true,\"tailscale_ip\":\"$(json_escape "$IP")\",\"detail\":\"$ESC\"}"; else echo "{\"ok\":false,\"error\":\"${ACTION} fehlgeschlagen\",\"detail\":\"$ESC\"}"; fi
`, qK, shQuoteSingle(extraFlags.String()))
}
