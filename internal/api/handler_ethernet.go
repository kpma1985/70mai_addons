package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"addon_installer/internal/ops"
)

func (s *Server) handleEthernet(w http.ResponseWriter, r *http.Request) {
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
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action == "" {
		action = "status"
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 30)
	defer cancel()

	var script string
	switch action {
	case "enable":
		script = ops.EthernetEnableScript
	case "disable":
		script = ops.EthernetDisableScript
	case "status":
		script = ops.EthernetStatusScript
	case "autostart-deploy":
		script = ops.EthernetAutostartDeploy
	case "autostart-remove":
		script = ops.EthernetAutostartRemove
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "ungültige action (enable|disable|status|autostart-deploy|autostart-remove)"})
		return
	}
	out, stderr, err := run.Run(ctx, script)
	writeJSON(w, map[string]any{
		"ok":      err == nil,
		"action":  action,
		"raw":     strings.TrimSpace(out),
		"stderr":  strings.TrimSpace(stderr),
		"error":   fmtErr(err),
	})
}
