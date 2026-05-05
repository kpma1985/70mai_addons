package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"

	"addon_installer/internal/ops"
	"addon_installer/internal/remote"
	"addon_installer/internal/tailscale"
)

func (s *Server) handleTailscaleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "Binaries werden auf diesem Rechner geladen und per SSH nach /mnt/sd geschoben — Transport 'http' unterstützt das nicht"})
		return
	}
	if strings.EqualFold(cfg.Transport, "telnet") {
		writeJSON(w, map[string]any{"ok": false, "error": "Binaries nur per SSH (zuverlässiger Push großer Dateien)"})
		return
	}
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Binaries: bitte Transport 'ssh' verwenden"})
		return
	}

	cacheDir, err := tailscale.CacheDir()
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 300)
	defer cancel()

	tsLocal, tsdLocal, staging, err := tailscale.PrepareBinaries(ctx, cacheDir, "", "")
	if staging != "" {
		defer os.RemoveAll(staging)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "cache": cacheDir})
		return
	}

	if err := run.PushTailscalePair(ctx, tsLocal, tsdLocal); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "cache": cacheDir})
		return
	}

	_, syncStderr, syncErr := run.Run(ctx, "sync")
	resp := map[string]any{
		"ok":    true,
		"msg":   "tailscale + tailscaled lokal von pkgs.tailscale.com geladen und per SSH nach /mnt/sd geschrieben",
		"cache": cacheDir,
	}
	if syncErr != nil {
		resp["sync_error"] = syncErr.Error()
		resp["sync_stderr"] = strings.TrimSpace(syncStderr)
	}
	writeJSON(w, resp)
}

func (s *Server) handleTailscaleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.EqualFold(cfg.Transport, "telnet") {
		writeJSON(w, map[string]any{"ok": false, "error": "tailscaled-Start nur per ssh"})
		return
	}
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "Manueller tailscaled-Start nur per ssh"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 45)
	defer cancel()
	out, stderr, err := run.RunScript(ctx, ops.TailscaleStart)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleTailscaleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 20)
	defer cancel()
	if strings.EqualFold(cfg.Transport, "telnet") {
		writeJSON(w, map[string]any{"ok": false, "error": "tailscale stop nur per ssh"})
		return
	}
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "HTTP-CGI-WebGUI ist entfernt; tailscale stop nur per ssh"})
		return
	}
	out, stderr, err := run.RunScript(ctx, ops.TailscaleStop)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleTailscaleUp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		connBody
		AuthKey                  string `json:"authkey"`
		Hostname                 string `json:"hostname"`
		Routes                   string `json:"routes"`
		ExitNode                 bool   `json:"exit_node"`
		AcceptRoutes             bool   `json:"accept_routes"`
		ClearTailscaleKeyAfterUp bool   `json:"clear_tailscale_key_after_up"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cur := s.CurrentConfig()
	// Fields fall back to stored values
	if req.AuthKey == "" {
		req.AuthKey = cur.TailscaleAuthKey
	}
	if req.Hostname == "" {
		req.Hostname = cur.TailscaleHostname
	}
	if req.Routes == "" {
		req.Routes = cur.TailscaleRoutes
	}
	if !req.ExitNode {
		req.ExitNode = cur.TailscaleExitNode
	}
	if !req.AcceptRoutes {
		req.AcceptRoutes = cur.TailscaleAcceptRoutes
	}
	cfg := applyConn(req.connBody, cur)

	// Persist all TS options
	upCfg := cur
	upCfg.TailscaleAuthKey = req.AuthKey
	upCfg.TailscaleHostname = req.Hostname
	upCfg.TailscaleRoutes = req.Routes
	upCfg.TailscaleExitNode = req.ExitNode
	upCfg.TailscaleAcceptRoutes = req.AcceptRoutes
	upCfg.ClearTailscaleKeyAfterUp = req.ClearTailscaleKeyAfterUp
	// Apply conn fields too
	if req.connBody.Host != "" {
		upCfg.Host = req.connBody.Host
	}
	_ = s.SaveCfg(upCfg)

	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 120)
	defer cancel()

	clearKeyIfNeeded := func(upOK bool) {
		if !upOK || !req.ClearTailscaleKeyAfterUp {
			return
		}
		afterCfg := s.CurrentConfig()
		afterCfg.TailscaleAuthKey = ""
		_ = s.SaveCfg(afterCfg)
	}

	if strings.EqualFold(cfg.Transport, "telnet") {
		writeJSON(w, map[string]any{"ok": false, "error": "tailscale up nur per ssh"})
		return
	}
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "HTTP-CGI-WebGUI ist entfernt; tailscale up nur per ssh"})
		return
	}
	out, stderr, err := run.RunScript(ctx, ops.TailscaleUpScript(ops.TailscaleUpOpts{
		AuthKey:      req.AuthKey,
		Hostname:     req.Hostname,
		Routes:       req.Routes,
		ExitNode:     req.ExitNode,
		AcceptRoutes: req.AcceptRoutes,
	}))
	ok := err == nil
	clearKeyIfNeeded(ok)
	writeJSON(w, map[string]any{"ok": ok, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleTailscaleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := withTimeout(r, 25)
	defer cancel()

	out, stderr, err := fetchCameraTailscaleStatus(ctx, cfg)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": out, "stderr": stderr, "error": fmtErr(err)})
}

func (s *Server) handleTailscaleRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Subnet-Routen-Erkennung braucht SSH"})
		return
	}
	ctx, cancel := withTimeout(r, 15)
	defer cancel()
	script := `#!/bin/sh
json_escape() { printf '%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
ip_to_int() {
  OLDIFS=$IFS; IFS=.; set -- $1; IFS=$OLDIFS
  echo $(( ($1 << 24) + ($2 << 16) + ($3 << 8) + $4 ))
}
int_to_ip() {
  N=$1
  echo "$(( (N >> 24) & 255 )).$(( (N >> 16) & 255 )).$(( (N >> 8) & 255 )).$(( N & 255 ))"
}
cidr_net() {
  IP=${1%/*}; PREFIX=${1#*/}
  [ "$IP" = "$1" ] && return 1
  [ "$PREFIX" -lt 1 ] 2>/dev/null && return 1
  [ "$PREFIX" -gt 30 ] 2>/dev/null && return 1
  IPN=$(ip_to_int "$IP")
  MASK=$(( (0xffffffff << (32 - PREFIX)) & 0xffffffff ))
  NET=$(( IPN & MASK ))
  printf '%s/%s' "$(int_to_ip "$NET")" "$PREFIX"
}
OUT=""
if command -v ip >/dev/null 2>&1; then
  LINES=$(ip -o -4 addr show scope global 2>/dev/null)
else
  LINES=""
fi
if [ -n "$LINES" ]; then
  echo "$LINES" | while read IDX IFACE FAM ADDR REST; do
    case "$IFACE" in lo|tailscale*|ts*|docker*|br-*|veth*) continue ;; esac
    NET=$(cidr_net "$ADDR") || continue
    printf '%s|%s\n' "$NET" "$IFACE"
  done
else
  ifconfig -a 2>/dev/null | awk '
    /^[a-zA-Z0-9_.:-]+/ {iface=$1; sub(/:/,"",iface)}
    /inet / && iface!="lo" {print $2 "|" iface}
  ' | while IFS='|' read IP IFACE; do
    case "$IP" in 127.*|"") continue ;; esac
    case "$IFACE" in tailscale*|ts*|docker*|br-*|veth*) continue ;; esac
    OLDIFS=$IFS; IFS=.; set -- $IP; IFS=$OLDIFS
    printf '%s.%s.%s.0/24|%s\n' "$1" "$2" "$3" "$IFACE"
  done
fi | awk '!seen[$1]++' > /tmp/ts_routes_$$
printf '{"ok":true,"routes":['
FIRST=1
while IFS='|' read NET IFACE; do
  [ -n "$NET" ] || continue
  [ "$FIRST" = 1 ] || printf ','
  FIRST=0
  printf '{"cidr":"%s","iface":"%s","label":"%s · %s"}' "$(json_escape "$NET")" "$(json_escape "$IFACE")" "$(json_escape "$NET")" "$(json_escape "$IFACE")"
done < /tmp/ts_routes_$$
rm -f /tmp/ts_routes_$$
printf ']}\n'
`
	run := newRunner(cfg)
	out, stderr, err := run.RunScript(ctx, script)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleTailscaleAutostart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var req struct {
		connBody
		Action string `json:"action"` // deploy | remove | status
	}
	_ = json.Unmarshal(b, &req)
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Tailscale-Autostart nur per SSH (Overlay-Partition beschreiben)"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "deploy"
	}
	var script string
	switch action {
	case "remove":
		script = ops.TailscaleAutostartRemove
	case "status":
		script = ops.TailscaleAutostartStatus
	default:
		script = ops.TailscaleAutostartDeploy
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 20)
	defer cancel()
	out, stderr, err := run.RunScript(ctx, script)
	writeJSON(w, map[string]any{
		"ok":     err == nil,
		"action": action,
		"raw":    strings.TrimSpace(out),
		"stderr": strings.TrimSpace(stderr),
		"error":  fmtErr(err),
	})
}
