package api

import (
	"encoding/json"
	"net/http"

	"addon_installer/internal/config"
)

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.CurrentConfig())
	case http.MethodPost:
		var c config.Config
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if c.Host == "" {
			c.Host = config.Default().Host
		}
		if c.Transport == "" {
			c.Transport = "ssh"
		}
		if err := s.SaveCfg(c); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
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
	run := newRunner(cfg)
	if err := run.Test(ctx); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "transport": cfg.Transport})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "transport": cfg.Transport})
}
