package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"addon_installer/internal/config"
	"addon_installer/internal/ops"
	"addon_installer/internal/remote"
)

// withTimeout returns a context derived from r.Context() with the given timeout in seconds.
func withTimeout(r *http.Request, seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
}

// newRunner creates a Runner for the given config.
func newRunner(cfg config.Config) *remote.Runner {
	return &remote.Runner{Cfg: cfg}
}

// ── Request body types ──────────────────────────────────────────────────────

type connBody struct {
	Host       string `json:"host"`
	Transport  string `json:"transport"`
	Password   string `json:"password"`
	SSHPort    int    `json:"ssh_port"`
	TelnetPort int    `json:"telnet_port"`
	// If set (JSON key present), replaces stored iptables_path; nil = unchanged.
	IptablesPath *string `json:"iptables_path,omitempty"`
}

type FileListRequest struct {
	connBody
	Path string `json:"path"`
}

type ActionRequest struct {
	connBody
	Action string `json:"action"`
}

type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Type  string `json:"type"`
	Perm  string `json:"perm"`
	Owner string `json:"owner,omitempty"`
	Group string `json:"group,omitempty"`
	Size  string `json:"size,omitempty"`
	Time  string `json:"time,omitempty"`
}

// ── Connection helpers ───────────────────────────────────────────────────────

func validPort(p int) bool { return p > 0 && p <= 65535 }

func applyConn(f connBody, base config.Config) config.Config {
	c := base
	if f.Host != "" {
		c.Host = f.Host
	}
	if f.Transport != "" {
		c.Transport = f.Transport
	}
	if f.SSHPort != 0 {
		if validPort(f.SSHPort) {
			c.SSHPort = f.SSHPort
		}
	}
	if f.TelnetPort != 0 {
		if validPort(f.TelnetPort) {
			c.TelnetPort = f.TelnetPort
		}
	}
	// Password: always take from body (may be empty)
	c.Password = f.Password
	if f.IptablesPath != nil {
		c.IptablesPath = strings.TrimSpace(*f.IptablesPath)
	}
	return c
}

func readConn(r *http.Request, base config.Config) (config.Config, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return config.Default(), err
	}
	var f connBody
	if len(strings.TrimSpace(string(b))) == 0 {
		return base, nil
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return config.Default(), err
	}
	return applyConn(f, base), nil
}

func readConnFromJSONString(s string, base config.Config) (config.Config, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return base, nil
	}
	var f connBody
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		return base, err
	}
	return applyConn(f, base), nil
}

// ── Camera status fetchers ───────────────────────────────────────────────────

// fetchCameraStatus reads status through the active command transport. The old
// camera CGI WebGUI is no longer part of the supported runtime.
func fetchCameraStatus(ctx context.Context, cfg config.Config) (raw, stderr string, err error) {
	run := &remote.Runner{Cfg: cfg}
	switch {
	case strings.EqualFold(cfg.Transport, "http"):
		return "", "", fmt.Errorf("transport http ist entfernt; nutze ssh oder telnet")
	case strings.EqualFold(cfg.Transport, "telnet"):
		out, se, e := run.Run(ctx, "ifconfig wlan0 2>/dev/null; ps 2>/dev/null | grep -E 'hostapd|wpa_supplicant|x800-addon' | grep -v grep")
		return strings.TrimSpace(out), strings.TrimSpace(se), e
	default:
		out, se, e := run.RunScript(ctx, ops.StatusScript)
		return strings.TrimSpace(out), strings.TrimSpace(se), e
	}
}

// fetchCameraTailscaleStatus retrieves the same data as POST /api/tailscale/status.
func fetchCameraTailscaleStatus(ctx context.Context, cfg config.Config) (raw, stderr string, err error) {
	if strings.EqualFold(cfg.Transport, "telnet") {
		return `{"ok":true,"unsupported":true,"transport":"telnet"}`, "", nil
	}
	run := &remote.Runner{Cfg: cfg}
	if strings.EqualFold(cfg.Transport, "http") {
		return "", "", fmt.Errorf("transport http ist entfernt; nutze ssh oder telnet")
	}
	out, se, e := run.RunScript(ctx, ops.TailscaleStatus)
	return strings.TrimSpace(out), strings.TrimSpace(se), e
}

// ── JSON helpers ─────────────────────────────────────────────────────────────

func parseJSONObject(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}
	}
	return m
}

func strVal(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func summarizeWLAN(m map[string]any) string {
	if len(m) == 0 {
		return "—"
	}
	mode := strings.TrimSpace(strVal(m["mode"]))
	ssid := strings.TrimSpace(strVal(m["ssid"]))
	if mode != "" && ssid != "" {
		return mode + ": " + ssid
	}
	if mode != "" {
		if mode == "ap" || mode == "client" {
			return mode + " (keine SSID)"
		}
		return mode
	}
	return "—"
}

func summarizeTailscaleCam(m map[string]any, rawFallback string) string {
	if len(m) == 0 {
		if strings.TrimSpace(rawFallback) != "" {
			return strings.TrimSpace(rawFallback)
		}
		return "—"
	}
	if v, ok := m["unsupported"].(bool); ok && v {
		return "ssh/http nötig"
	}
	if v, ok := m["installed"].(bool); ok && !v {
		return "nicht installiert"
	}
	run, _ := m["running"].(bool)
	ip, _ := m["tailscale_ip"].(string)
	ip = strings.TrimSpace(ip)
	if run && ip != "" {
		return ip
	}
	if run {
		return "läuft"
	}
	if _, ok := m["installed"].(bool); ok {
		return "installiert, Daemon aus"
	}
	if strings.TrimSpace(rawFallback) != "" {
		return strings.TrimSpace(rawFallback)
	}
	return "—"
}

// ── Validation helpers ───────────────────────────────────────────────────────

func validateAPInput(ssid, pwd string) string {
	if strings.TrimSpace(ssid) == "" {
		return "ssid fehlt"
	}
	if len(ssid) > 31 {
		return "SSID zu lang (max. 31 Zeichen für usr1)"
	}
	if pwd != "" && (len(pwd) < 8 || len(pwd) > 31) {
		return "WPA-Passwort muss leer oder 8-31 Zeichen lang sein (usr1-Slot)"
	}
	return ""
}

// ListenExposesLAN returns true when the given listen address is reachable from the LAN.
func ListenExposesLAN(addr string) bool {
	addr = strings.TrimSpace(addr)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.HasPrefix(addr, ":")
	}
	if host == "" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return false
		}
		return true
	}
	return true
}

// ── Shell / path helpers ─────────────────────────────────────────────────────

func shellQuoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func shellIPT(iptExec string) string {
	iptExec = strings.TrimSpace(iptExec)
	if iptExec == "" {
		return "iptables"
	}
	return shellQuoteSingle(iptExec)
}

func cleanRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	p = strings.ReplaceAll(p, "\x00", "")
	p = strings.ReplaceAll(p, "\n", "")
	p = strings.ReplaceAll(p, "\r", "")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func parentRemotePath(p string) string {
	p = cleanRemotePath(p)
	if p == "/" {
		return "/"
	}
	return path.Dir(p)
}

func parseLSListing(base, raw string) []FileEntry {
	base = cleanRemotePath(base)
	var entries []FileEntry
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		name := strings.Join(fields[8:], " ")
		if name == "." {
			continue
		}
		if idx := strings.Index(name, " -> "); idx >= 0 {
			name = name[:idx]
		}
		entryType := "file"
		switch fields[0][0] {
		case 'd':
			entryType = "dir"
		case 'l':
			entryType = "link"
		case 'c', 'b':
			entryType = "device"
		case 's':
			entryType = "socket"
		case 'p':
			entryType = "pipe"
		}
		entryPath := path.Join(base, name)
		if name == ".." {
			entryPath = parentRemotePath(base)
			entryType = "dir"
		}
		entries = append(entries, FileEntry{
			Name:  name,
			Path:  entryPath,
			Type:  entryType,
			Perm:  fields[0],
			Owner: fields[2],
			Group: fields[3],
			Size:  fields[4],
			Time:  strings.Join(fields[5:8], " "),
		})
	}
	return entries
}

func parseSystemSections(raw string) map[string]string {
	sections := map[string]string{}
	current := "raw"
	var b strings.Builder
	flush := func() {
		if strings.TrimSpace(b.String()) != "" {
			sections[current] = strings.TrimSpace(b.String())
		}
		b.Reset()
	}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	return sections
}

func systemStatusCommand() string {
	script := `section(){ echo; echo "### $1"; }
run(){ section "$1"; shift; "$@" 2>&1 || true; }
snap_proc_cpu() {
  OUT=$1
  : > "$OUT"
  for p in /proc/[0-9]*; do
    [ -r "$p/stat" ] || continue
    STAT=$(cat "$p/stat" 2>/dev/null) || continue
    PID=${p##*/}
    COMM=${STAT#* (}
    COMM=${COMM%%) *}
    REST=${STAT#*) }
    set -- $REST
    STATE=$1
    UTIME=${12:-0}
    STIME=${13:-0}
    case "$UTIME:$STIME" in *[!0-9:]*|"":*) TOTAL=0 ;; *) TOTAL=$((UTIME + STIME)) ;; esac
    printf '%s %s %s %s\n' "$PID" "$TOTAL" "$STATE" "$COMM" >> "$OUT"
  done
}
section "load triage"
echo "--- loadavg"; cat /proc/loadavg 2>/dev/null || true
echo "--- uptime"; cat /proc/uptime 2>/dev/null || true
echo "--- vmstat"; cat /proc/vmstat 2>/dev/null | grep -E '^(procs_|pgfault|pgmajfault|pswp|nr_dirty|nr_writeback|nr_uninterruptible)' || true
echo "--- runnable or blocked tasks"
for p in /proc/[0-9]*; do
  [ -r "$p/stat" ] || continue
  STAT=$(cat "$p/stat" 2>/dev/null) || continue
  PID=${p##*/}
  COMM=${STAT#* (}; COMM=${COMM%%) *}
  REST=${STAT#*) }; set -- $REST; STATE=$1
  case "$STATE" in R|D) printf '%s %s %s\n' "$PID" "$STATE" "$COMM" ;; esac
done | head -30
echo "--- top cpu over 3s (jiffies)"
S1=/tmp/load_triage_1_$$.txt
S2=/tmp/load_triage_2_$$.txt
snap_proc_cpu "$S1"
sleep 3
snap_proc_cpu "$S2"
awk 'NR==FNR{old[$1]=$2; next} {d=$2-old[$1]; if (d>0) printf "%8d pid=%-6s state=%s cmd=%s\n", d, $1, $3, substr($0, index($0,$4))}' "$S1" "$S2" 2>/dev/null | sort -nr | head -20
rm -f "$S1" "$S2"
echo "--- addon/tailscale/flespi pids"
ps 2>/dev/null | grep -E 'x800-addon|tailscale|flespi|wpa_supplicant|hostapd|main_app|Nvt|curl|wget' | grep -v grep || true
section "identity"; { hostname; uname -a; cat /proc/version 2>/dev/null; date 2>/dev/null; uptime 2>/dev/null; } 2>&1
run "cpu" cat /proc/cpuinfo
run "memory" cat /proc/meminfo
run "storage df" df -h
run "mounts" mount
run "partitions" cat /proc/partitions
run "network ifconfig" ifconfig -a
run "routes" route -n
run "dns" cat /etc/resolv.conf
run "processes" ps
run "kernel modules" cat /proc/modules
run "init scripts" ls -la /etc/init.d
run "customfw sd" ls -la /mnt/sd
run "addon installer process" sh -c 'ps 2>/dev/null | grep x800-addon-installer | grep -v grep || true'
run "addon installer log" sh -c '[ -e /mnt/sd/addon_webui.log ] && tail -80 /mnt/sd/addon_webui.log || true'
run "tailscale" sh -c '[ -x /mnt/sd/tailscale ] && /mnt/sd/tailscale --socket=/var/run/tailscale/tailscaled.sock status || echo tailscale-binary-fehlt'
run "logs" sh -c 'for f in /mnt/sd/customfw.log /mnt/sd/last_wifi_error.txt /var/log/messages; do [ -e "$f" ] && { echo "--- $f"; tail -80 "$f"; }; done'`
	return "sh -c " + shellQuoteSingle(script)
}

// apForwardingCommand builds a shell script for Proxy-ARP forwarding (no iptables/NAT needed).
// hostapd is NEVER restarted — only udhcpd switches, AP stays visible.
// Watchdog runs via setsid independent of the SSH process and reverts after 60 s if needed.
func apForwardingCommand(action string, _ string) string {
	script := `export PATH="/sbin:/usr/sbin:/bin:/usr/bin:$PATH"
AP_IF=wlan0
for cand in wlan0 wlan1; do
  if [ -d "/sys/class/net/$cand" ]; then AP_IF=$cand; break; fi
done
ACTION=` + shellQuoteSingle(action) + `

get_ip() { ifconfig "$1" 2>/dev/null | grep -oE 'inet (addr:)?[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | head -1; }
net_prefix() { echo "$1" | cut -d. -f1-3; }
ip_last()    { echo "$1" | cut -d. -f4; }
deauth_all() { hostapd_cli -p /var/run/hostapd -i "$AP_IF" deauthenticate ff:ff:ff:ff:ff:ff 2>/dev/null || true; }

status() {
  echo "### AP Proxy-ARP Forwarding Status"
  echo "ap_if=$AP_IF"
  printf "ip_forward="; cat /proc/sys/net/ipv4/ip_forward 2>/dev/null || echo "?"
  printf "proxy_arp=$AP_IF="; cat /proc/sys/net/ipv4/conf/"$AP_IF"/proxy_arp 2>/dev/null || echo "?"
  echo
  echo "### $AP_IF"
  ifconfig "$AP_IF" 2>&1 || true
  echo
  echo "### uplinks"
  for IF in usb_4g0 usb0 eth0; do
    IP=$(get_ip "$IF")
    [ -n "$IP" ] && echo "$IF: $IP" || true
  done
  echo
  echo "### routes"
  route -n 2>&1 || true
  echo
  echo "### dhcp (forwarding)"
  [ -e /tmp/udhcpd_fwd.conf ] && head -8 /tmp/udhcpd_fwd.conf || echo "(nicht aktiv)"
  echo
  echo "### dhcp leases"
  cat /tmp/udhcpd_fwd.leases 2>/dev/null | head -10 || echo "(keine)"
}

# DISABLE --------------------------------------------------------------------
if [ "$ACTION" = "disable" ]; then
  echo "### Forwarding deaktivieren"
  rm -f /tmp/fwd_ok
  killall udhcpd 2>/dev/null; sleep 1
  echo 0 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true
  for IF in all "$AP_IF" wlan0 wlan1 usb_4g0 usb0 eth0; do
    [ -d "/proc/sys/net/ipv4/conf/$IF" ] || continue
    echo 0 > /proc/sys/net/ipv4/conf/$IF/proxy_arp 2>/dev/null || true
    echo 0 > /proc/sys/net/ipv4/conf/$IF/proxy_arp_pvlan 2>/dev/null || true
  done
  ip route show dev "$AP_IF" 2>/dev/null | while read DST REST; do
    case "$DST" in
      */24|*/32) ip route del "$DST" dev "$AP_IF" 2>/dev/null || true ;;
    esac
  done
  ifconfig "$AP_IF" 192.168.0.1 netmask 255.255.255.0 up 2>&1 || true
  ip neigh flush dev "$AP_IF" 2>/dev/null || true
  udhcpd -S /etc/udhcpdw.conf 2>/dev/null || true
  deauth_all
  echo "wlan0=192.168.0.1, Original-DHCP gestartet, Clients deauth."
  status
  exit 0
fi

# STATUS ---------------------------------------------------------------------
if [ "$ACTION" = "status" ]; then
  status
  exit 0
fi

# ENABLE ---------------------------------------------------------------------
echo "### Proxy-ARP Forwarding aktivieren"

fail() { echo "FEHLER: $1"; status; exit 1; }

# Phase 1: Validierung (keine Aenderungen) -----------------------------------
echo "--- Phase 1: Validierung ---"

UPLINK=; UPLINK_IP=
for IF in usb_4g0 usb0 eth0; do
  IP=$(get_ip "$IF")
  if [ -n "$IP" ]; then UPLINK=$IF; UPLINK_IP=$IP; break; fi
done
[ -n "$UPLINK" ] || fail "kein Uplink mit IP (usb_4g0/usb0/eth0)"
echo "  Uplink: $UPLINK ($UPLINK_IP)"

PREFIX=$(net_prefix "$UPLINK_IP")
UPLINK_LAST=$(ip_last "$UPLINK_IP")

WLAN_IP=
for last in 2 3 4 6 7 8 9 10; do
  [ "$last" = "$UPLINK_LAST" ] && continue
  CAND="$PREFIX.$last"
  arp -n 2>/dev/null | grep -q "^$CAND " && continue
  WLAN_IP=$CAND; break
done
[ -n "$WLAN_IP" ] || fail "keine freie IP in $PREFIX.2-.10"
GW="$PREFIX.1"
echo "  wlan0 geplant: $WLAN_IP/24  Gateway: $GW"

echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null \
  || fail "ip_forward nicht beschreibbar"
echo 0 > /proc/sys/net/ipv4/ip_forward 2>/dev/null

DHCP_START="$PREFIX.200"
DHCP_END="$PREFIX.220"
cat > /tmp/udhcpd_fwd.conf << DHCPEOF
interface $AP_IF
start $DHCP_START
end $DHCP_END
lease_file /tmp/udhcpd_fwd.leases
option subnet 255.255.255.0
opt router $WLAN_IP
opt dns 8.8.8.8 8.8.4.4
option lease 3600
DHCPEOF
echo "--- Validierung OK ---"

# Phase 2: Watchdog starten BEVOR IP-Aenderung -------------------------------
rm -f /tmp/fwd_ok
cat > /tmp/fwd_watchdog.sh << 'WDOG'
#!/bin/sh
sleep 60
[ -f /tmp/fwd_ok ] && exit 0
echo 0 > /proc/sys/net/ipv4/ip_forward 2>/dev/null
for _IF in all wlan0 wlan1 usb_4g0 usb0 eth0; do
  [ -d "/proc/sys/net/ipv4/conf/$_IF" ] || continue
  echo 0 > /proc/sys/net/ipv4/conf/$_IF/proxy_arp 2>/dev/null
  echo 0 > /proc/sys/net/ipv4/conf/$_IF/proxy_arp_pvlan 2>/dev/null
done
ifconfig wlan0 192.168.0.1 netmask 255.255.255.0 up 2>/dev/null
killall udhcpd 2>/dev/null; sleep 1
udhcpd -S /etc/udhcpdw.conf 2>/dev/null
hostapd_cli -p /var/run/hostapd -i wlan0 deauthenticate ff:ff:ff:ff:ff:ff 2>/dev/null
arping -I wlan0 -c 2 -U 192.168.0.1 2>/dev/null
WDOG
chmod +x /tmp/fwd_watchdog.sh
nohup sh /tmp/fwd_watchdog.sh >/dev/null 2>&1 &
echo "  Watchdog PID $! — Auto-Revert in 60 s falls SSH bricht"

# Phase 3: Setup-Script schreiben + via setsid detached starten -------------
echo "--- Phase 3: Setup-Script deployen ---"
cat > /tmp/fwd_setup.sh << FWDEOF
#!/bin/sh
sleep 2
killall udhcpd 2>/dev/null; sleep 1
rm -f /tmp/udhcpd_fwd.leases
ifconfig $AP_IF $WLAN_IP netmask 255.255.255.0 up 2>/dev/null || exit 1
ip route del $PREFIX.0/24 dev $AP_IF 2>/dev/null || true
ip route add $PREFIX.0/24 dev $AP_IF metric 1000 2>/dev/null || true
echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true
echo 0 > /proc/sys/net/ipv4/conf/$AP_IF/proxy_arp 2>/dev/null || true
echo 0 > /proc/sys/net/ipv4/conf/$AP_IF/proxy_arp_pvlan 2>/dev/null || true
echo 1 > /proc/sys/net/ipv4/conf/$UPLINK/proxy_arp 2>/dev/null || true
echo 1 > /proc/sys/net/ipv4/conf/$UPLINK/proxy_arp_pvlan 2>/dev/null || true
echo 0 > /proc/sys/net/ipv4/conf/all/proxy_arp_pvlan 2>/dev/null || true
_i=200; while [ $_i -le 220 ]; do
  ip route add $PREFIX.$_i/32 dev $AP_IF 2>/dev/null || true
  _i=$((_i+1))
done
ip neigh flush dev $AP_IF 2>/dev/null || true
udhcpd -S /tmp/udhcpd_fwd.conf 2>/dev/null
sleep 2
ip neigh flush dev $AP_IF 2>/dev/null || true
hostapd_cli -p /var/run/hostapd -i $AP_IF deauthenticate ff:ff:ff:ff:ff:ff 2>/dev/null || true
touch /tmp/fwd_ok
echo "Setup OK: $AP_IF=$(ifconfig $AP_IF 2>/dev/null | grep -oE 'inet [0-9.]+' | cut -d' ' -f2) proxy_arp=$(cat /proc/sys/net/ipv4/conf/$AP_IF/proxy_arp 2>/dev/null) pvlan_all=$(cat /proc/sys/net/ipv4/conf/all/proxy_arp_pvlan 2>/dev/null)"
FWDEOF
chmod +x /tmp/fwd_setup.sh
nohup sh /tmp/fwd_setup.sh >/tmp/fwd_setup.log 2>&1 &
echo "  Setup-PID $! gestartet (nohup sh)"
echo "  Neue Kamera-IP: $WLAN_IP (in ~3 s aktiv)"
echo "  AP-Clients: DHCP $DHCP_START-$DHCP_END, Gateway $GW"
echo "  SSID neu verbinden — Installer-Host auf $WLAN_IP setzen."`
	return "sh -c " + shellQuoteSingle(script)
}

// keep shellIPT available (used in apForwardingCommand context, kept for completeness)
var _ = shellIPT
