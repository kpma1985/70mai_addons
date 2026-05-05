package api

import (
	"net/http"
	"strings"

	"addon_installer/internal/remote"
)

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := withTimeout(r, 45)
	defer cancel()

	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{
			"ok":    false,
			"error": "transport http ist entfernt; nutze ssh oder telnet",
		})
		return
	}

	run := newRunner(cfg)
	out, stderr, err := run.Run(ctx, systemStatusCommand())
	writeJSON(w, map[string]any{
		"ok":        err == nil,
		"transport": cfg.Transport,
		"raw":       strings.TrimSpace(out),
		"sections":  parseSystemSections(out),
		"stderr":    strings.TrimSpace(stderr),
		"error":     fmtErr(err),
	})
}

func (s *Server) handleSystemReboot(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{"ok": false, "error": "Reboot nur per ssh"})
		return
	}
	ctx, cancel := withTimeout(r, 10)
	defer cancel()
	run := newRunner(cfg)
	out, stderr, err := run.Run(ctx, "sync; (sleep 2 && /sbin/reboot) >/dev/null 2>&1 & echo reboot")
	writeJSON(w, map[string]any{
		"ok":        err == nil,
		"raw":       strings.TrimSpace(out),
		"stderr":    strings.TrimSpace(stderr),
		"error":     fmtErr(err),
		"transport": cfg.Transport,
		"reboot":    err == nil,
	})
}
