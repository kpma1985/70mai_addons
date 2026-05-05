package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"addon_installer/internal/ops"
	"addon_installer/internal/remote"
)

func (s *Server) handleWifiVendorCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		writeJSON(w, map[string]any{"ok": false, "verified": false, "error": "Host fehlt"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 1200 * time.Millisecond}
	client := &http.Client{
		Timeout: 2500 * time.Millisecond,
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
	url := "http://" + host + "/cgi-bin/getwifi.cgi"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "verified": false, "error": err.Error()})
		return
	}
	res, err := client.Do(req)
	if err != nil {
		writeJSON(w, map[string]any{
			"ok":       false,
			"verified": false,
			"error":    "70mai-WiFi-Status nicht prüfbar: " + err.Error(),
			"hint":     "In der 70mai-App muss WiFi / WiFi immer aktiv vor dem Client-Modus aktiviert sein.",
		})
		return
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	raw := strings.TrimSpace(string(b))
	lower := strings.ToLower(raw)

	if strings.Contains(raw, `"ResultCode":"-6677"`) || strings.Contains(raw, `"ResultCode":-6677`) {
		writeJSON(w, map[string]any{
			"ok":          false,
			"verified":    false,
			"auth_needed": true,
			"raw":         raw,
			"error":       "70mai-HTTP-API verlangt App-Session/Auth (ResultCode -6677)",
			"hint":        "Bitte in der 70mai-App WiFi / WiFi immer aktiv einschalten und danach erneut versuchen.",
		})
		return
	}

	disabledMarkers := []string{
		`"wifi_enable":0`, `"wifi_enable":"0"`,
		`"wifi_en":0`, `"wifi_en":"0"`,
		`"wifiboot":0`, `"wifiboot":"0"`,
		`"wifi_boot":0`, `"wifi_boot":"0"`,
		`"always":0`, `"always":"0"`,
		`"enable":0`, `"enable":"0"`,
		`"on":0`, `"on":"0"`,
	}
	for _, marker := range disabledMarkers {
		if strings.Contains(lower, marker) {
			writeJSON(w, map[string]any{
				"ok":       false,
				"verified": true,
				"enabled":  false,
				"raw":      raw,
				"error":    "70mai-WiFi ist laut Vendor-API nicht aktiv",
				"hint":     "Bitte in der 70mai-App WiFi / WiFi immer aktiv einschalten.",
			})
			return
		}
	}
	enabledMarkers := []string{
		`"wifi_enable":1`, `"wifi_enable":"1"`,
		`"wifi_en":1`, `"wifi_en":"1"`,
		`"wifiboot":1`, `"wifiboot":"1"`,
		`"wifi_boot":1`, `"wifi_boot":"1"`,
		`"always":1`, `"always":"1"`,
		`"enable":1`, `"enable":"1"`,
		`"on":1`, `"on":"1"`,
	}
	for _, marker := range enabledMarkers {
		if strings.Contains(lower, marker) {
			writeJSON(w, map[string]any{"ok": true, "verified": true, "enabled": true, "raw": raw})
			return
		}
	}
	writeJSON(w, map[string]any{
		"ok":       false,
		"verified": false,
		"raw":      raw,
		"error":    "70mai-WiFi-Status konnte aus der Vendor-API-Antwort nicht erkannt werden",
		"hint":     "Bitte in der 70mai-App WiFi / WiFi immer aktiv einschalten.",
	})
}

func (s *Server) handleWifiStatus(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := withTimeout(r, 15)
	defer cancel()
	out, stderr, err := run.Run(ctx, ops.WifiStatusScript)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiWrapper(w http.ResponseWriter, r *http.Request) {
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
		Action string `json:"action"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 30)
	defer cancel()
	var script string
	switch req.Action {
	case "install":
		script = ops.WifiWrapperInstallScript
	case "remove":
		script = ops.WifiWrapperRemoveScript
	case "status":
		script = ops.WifiWrapperStatusScript
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "ungültige action (install|remove|status)"})
		return
	}
	out, stderr, err := run.Run(ctx, script)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiConnect(w http.ResponseWriter, r *http.Request) {
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
		SSID     string `json:"ssid"`
		PWD      string `json:"pwd"`
		StaticIP string `json:"staticip"`
		Mask     string `json:"mask"`
		GW       string `json:"gw"`
		DNS      string `json:"dns"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.SSID = strings.TrimSpace(req.SSID)
	if req.SSID == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "SSID fehlt"})
		return
	}
	if len(req.SSID) > 31 {
		writeJSON(w, map[string]any{"ok": false, "error": "SSID zu lang (max. 31 Zeichen)"})
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 30)
	defer cancel()
	script := ops.WifiConnectScript(req.SSID, req.PWD, req.StaticIP, req.Mask, req.GW, req.DNS)
	out, stderr, err := run.RunScript(ctx, script)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiDisconnect(w http.ResponseWriter, r *http.Request) {
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
	out, stderr, err := run.Run(ctx, ops.WifiDisconnectScript)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiHealth(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{"ok": false, "error": "Health-Check läuft auf der Kamera und benötigt SSH"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 20)
	defer cancel()
	out, stderr, err := run.Run(ctx, ops.WifiHealthScript)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiScan(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{"ok": false, "error": "Scan nur per SSH verfügbar"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 30)
	defer cancel()
	out, stderr, err := run.Run(ctx, ops.WifiScanScript)
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}

func (s *Server) handleWifiUploadWpalib(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{"ok": false, "error": "wpalib-Upload nur per SSH"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 120)
	defer cancel()
	if err := run.PushWpalib(ctx, s.WpalibFS); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "msg": "wpalib nach /mnt/sd/wpalib/ hochgeladen"})
}
