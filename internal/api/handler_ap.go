package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"addon_installer/internal/ops"
)

func (s *Server) handleAPForwarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req ActionRequest
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "UP04-Forwarding braucht ssh oder telnet"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "enable"
	}
	if action != "enable" && action != "disable" && action != "status" {
		writeJSON(w, map[string]any{"ok": false, "error": "ungültige action (enable|disable|status)"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 90)
	defer cancel()
	out, stderr, err := run.Run(ctx, apForwardingCommand(action, cfg.IptablesPath))
	writeJSON(w, map[string]any{
		"ok":        err == nil,
		"action":    action,
		"transport": cfg.Transport,
		"raw":       strings.TrimSpace(out),
		"stderr":    strings.TrimSpace(stderr),
		"error":     fmtErr(err),
	})
}

func (s *Server) handleAP(w http.ResponseWriter, r *http.Request) {
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
		SSID string `json:"ssid"`
		PWD  string `json:"pwd"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.SSID = strings.TrimSpace(req.SSID)
	if msg := validateAPInput(req.SSID, req.PWD); msg != "" {
		writeJSON(w, map[string]any{"ok": false, "error": msg})
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 90)
	defer cancel()

	if strings.EqualFold(cfg.Transport, "telnet") {
		writeJSON(w, map[string]any{"ok": false, "error": "AP nur per ssh"})
		return
	}
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "HTTP-CGI-WebGUI ist entfernt; AP nur per ssh"})
		return
	}

	out, stderr, err := run.RunScript(ctx, ops.APApplyScript(req.SSID, req.PWD))
	writeJSON(w, map[string]any{"ok": err == nil, "raw": strings.TrimSpace(out), "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
}
