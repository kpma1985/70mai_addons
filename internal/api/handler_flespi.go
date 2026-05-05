package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"addon_installer/internal/config"
	"addon_installer/internal/flespi"
	"addon_installer/internal/ops"
	"addon_installer/internal/remote"
)

func parseFlespiDaemonRaw(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	status := map[string]any{}
	if raw == "" {
		return status
	}
	if json.Unmarshal([]byte(raw), &status) == nil {
		if status["running"] == true {
			status["installed"] = true
		}
		return status
	}
	boolField := func(key string) {
		if strings.Contains(raw, `"`+key+`":true`) {
			status[key] = true
		} else if strings.Contains(raw, `"`+key+`":false`) {
			status[key] = false
		}
	}
	stringField := func(key string) {
		prefix := `"` + key + `":"`
		i := strings.Index(raw, prefix)
		if i < 0 {
			return
		}
		rest := raw[i+len(prefix):]
		j := strings.Index(rest, `"`)
		if j >= 0 {
			status[key] = rest[:j]
		}
	}
	intField := func(key string) {
		prefix := `"` + key + `":`
		i := strings.Index(raw, prefix)
		if i < 0 {
			return
		}
		rest := raw[i+len(prefix):]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 0 {
			if n, err := strconv.Atoi(rest[:j]); err == nil {
				status[key] = n
			}
		}
	}
	for _, key := range []string{"ok", "running", "installed", "conf_ok", "device_ok", "autostart"} {
		boolField(key)
	}
	for _, key := range []string{"device_id", "pid", "last_log"} {
		stringField(key)
	}
	intField("interval")
	if status["running"] == true {
		status["installed"] = true
	}
	return status
}

func (s *Server) handleFlespiReset(w http.ResponseWriter, r *http.Request) {
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
		RemoveFromCamera bool `json:"remove_from_camera"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())

	next := config.StripFlespi(s.CurrentConfig())
	if err := s.SaveCfg(next); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	resp := map[string]any{
		"ok":          true,
		"local_reset": true,
	}
	if req.RemoveFromCamera && remote.IsSSHTransport(cfg.Transport) {
		run := newRunner(cfg)
		ctx, cancel := withTimeout(r, 60)
		defer cancel()
		out, stderr, runErr := run.RunScript(ctx, ops.FlespiCameraConfigResetScript)
		resp["camera_raw"] = strings.TrimSpace(out)
		resp["camera_stderr"] = strings.TrimSpace(stderr)
		if runErr != nil {
			resp["camera_error"] = runErr.Error()
		}
	} else if req.RemoveFromCamera {
		resp["camera_skipped"] = "nur SSH"
	}
	writeJSON(w, resp)
}

func (s *Server) handleFlespiTest(w http.ResponseWriter, r *http.Request) {
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
		Token string `json:"token"`
		Host  string `json:"host"`
	}
	_ = json.Unmarshal(b, &req)
	cur := s.CurrentConfig()
	tok := strings.TrimSpace(req.Token)
	if tok == "" {
		tok = strings.TrimSpace(cur.FlespiToken)
	}
	base := strings.TrimSpace(req.Host)
	if base == "" {
		base = strings.TrimSpace(cur.FlespiHost)
	}
	ctx, cancel := withTimeout(r, 25)
	defer cancel()
	code, preview, err := flespi.VerifyToken(ctx, tok, base)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "http_status": code, "preview": preview, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "http_status": code, "preview": preview})
}

func (s *Server) ensureFlespiDevice(ctxReq *http.Request, token, host, name, ident, deviceTypeID string) (string, bool, string, error) {
	cur := s.CurrentConfig()
	tok := strings.TrimSpace(token)
	if tok == "" {
		tok = strings.TrimSpace(cur.FlespiToken)
	}
	base := strings.TrimSpace(host)
	if base == "" {
		base = strings.TrimSpace(cur.FlespiHost)
	}
	deviceName := strings.TrimSpace(name)
	if deviceName == "" {
		if strings.TrimSpace(cur.TailscaleHostname) != "" {
			deviceName = "70mai X800 " + strings.TrimSpace(cur.TailscaleHostname)
		} else {
			deviceName = "70mai X800"
		}
	}
	deviceIdent := strings.TrimSpace(ident)
	if deviceIdent == "" {
		deviceIdent = strings.TrimSpace(cur.FlespiIdent)
	}
	if deviceIdent == "" {
		if strings.TrimSpace(cur.TailscaleHostname) != "" {
			deviceIdent = "x800-" + strings.TrimSpace(cur.TailscaleHostname)
		} else if strings.TrimSpace(cur.Host) != "" {
			deviceIdent = "x800-" + strings.NewReplacer(".", "-", ":", "-", "/", "-").Replace(strings.TrimSpace(cur.Host))
		} else {
			deviceIdent = "x800-addon-installer"
		}
	}
	typeID := strings.TrimSpace(deviceTypeID)
	if typeID == "" {
		typeID = strings.TrimSpace(cur.FlespiDeviceTypeID)
	}

	ctx, cancel := withTimeout(ctxReq, 35)
	defer cancel()
	res, err := flespi.EnsureHTTPDevice(ctx, tok, base, deviceName, deviceIdent, typeID)
	if err != nil {
		return "", false, res.Preview, err
	}
	deviceID := strconv.FormatInt(res.Device.ID, 10)
	next := cur
	next.FlespiToken = tok
	next.FlespiHost = base
	next.FlespiDeviceID = deviceID
	next.FlespiDeviceTypeID = typeID
	next.FlespiIdent = deviceIdent
	_ = s.SaveCfg(next)
	return deviceID, res.Created, res.Preview, nil
}

func (s *Server) handleFlespiDevice(w http.ResponseWriter, r *http.Request) {
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
		Token  string `json:"token"`
		Host   string `json:"host"`
		Name   string `json:"name"`
		Ident  string `json:"ident"`
		TypeID string `json:"device_type_id"`
	}
	_ = json.Unmarshal(b, &req)
	deviceID, created, preview, err := s.ensureFlespiDevice(r, req.Token, req.Host, req.Name, req.Ident, req.TypeID)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "preview": preview})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "device_id": deviceID, "created": created, "preview": preview})
}

func (s *Server) handleFlespiDeploy(w http.ResponseWriter, r *http.Request) {
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
		IncludeToken bool   `json:"include_token"`
		Token        string `json:"token"`
		DeviceID     string `json:"device_id"`
		DeviceTypeID string `json:"device_type_id"`
		Interval     int    `json:"interval"`
		Autostart    bool   `json:"autostart"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Flespi-Deploy nur per SSH"})
		return
	}
	cur := s.CurrentConfig()
	tok := ""
	if req.IncludeToken || req.Autostart {
		tok = strings.TrimSpace(req.Token)
		if tok == "" {
			tok = strings.TrimSpace(cur.FlespiToken)
		}
	}
	if req.Autostart && tok == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "Autostart braucht einen Token. Der Token wird automatisch in /mnt/sd/flespi.conf gespeichert."})
		return
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	if deviceID == "" {
		deviceID = strings.TrimSpace(cur.FlespiDeviceID)
	}
	created := false
	preview := ""
	if (req.IncludeToken || req.Autostart) && deviceID == "" {
		var ensureErr error
		deviceID, created, preview, ensureErr = s.ensureFlespiDevice(r, tok, cur.FlespiHost, "", cur.FlespiIdent, req.DeviceTypeID)
		if ensureErr != nil {
			writeJSON(w, map[string]any{"ok": false, "error": ensureErr.Error(), "preview": preview})
			return
		}
	}
	interval := req.Interval
	if interval <= 0 {
		interval = cur.FlespiInterval
	}
	if interval <= 0 {
		interval = 30
	}
	next := cur
	if tok != "" {
		next.FlespiToken = tok
	}
	if deviceID != "" {
		next.FlespiDeviceID = deviceID
	}
	next.FlespiInterval = interval
	next.FlespiAutostart = req.Autostart
	if strings.TrimSpace(req.DeviceTypeID) != "" {
		next.FlespiDeviceTypeID = strings.TrimSpace(req.DeviceTypeID)
	}
	_ = s.SaveCfg(next)
	ident := strings.TrimSpace(next.FlespiIdent)
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 45)
	defer cancel()
	out, stderr, err := run.RunScript(ctx, ops.FlespiDeployScript(tok, deviceID, ident, next.FlespiHost, interval, req.Autostart))
	writeJSON(w, map[string]any{
		"ok":             err == nil,
		"raw":            strings.TrimSpace(out),
		"stderr":         strings.TrimSpace(stderr),
		"error":          fmtErr(err),
		"device_id":      deviceID,
		"device_created": created,
		"device_preview": preview,
		"hint":           "Deploy installiert flespi.conf und flespi-daemon.sh. Bei Autostart wird der Token automatisch auf SD gespeichert.",
		"transport":      cfg.Transport,
	})
}

func (s *Server) handleFlespiDaemon(w http.ResponseWriter, r *http.Request) {
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
		Action       string `json:"action"`
		Token        string `json:"token"`
		DeviceID     string `json:"device_id"`
		DeviceTypeID string `json:"device_type_id"`
		Interval     int    `json:"interval"`
		Autostart    bool   `json:"autostart"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Flespi-Daemon nur per SSH"})
		return
	}

	cur := s.CurrentConfig()
	action := strings.TrimSpace(req.Action)
	var script string
	created := false
	preview := ""
	switch action {
	case "install":
		tok := strings.TrimSpace(req.Token)
		if tok == "" {
			tok = strings.TrimSpace(cur.FlespiToken)
		}
		if req.Autostart && tok == "" {
			writeJSON(w, map[string]any{"ok": false, "error": "Autostart braucht einen Token. Der Token wird automatisch in /mnt/sd/flespi.conf gespeichert."})
			return
		}
		deviceID := strings.TrimSpace(req.DeviceID)
		if deviceID == "" {
			deviceID = strings.TrimSpace(cur.FlespiDeviceID)
		}
		if tok != "" && deviceID == "" {
			var ensureErr error
			deviceID, created, preview, ensureErr = s.ensureFlespiDevice(r, tok, cur.FlespiHost, "", cur.FlespiIdent, req.DeviceTypeID)
			if ensureErr != nil {
				writeJSON(w, map[string]any{"ok": false, "error": ensureErr.Error(), "preview": preview})
				return
			}
		}
		next := cur
		if tok != "" {
			next.FlespiToken = tok
		}
		if deviceID != "" {
			next.FlespiDeviceID = deviceID
		}
		interval := req.Interval
		if interval <= 0 {
			interval = cur.FlespiInterval
		}
		if interval <= 0 {
			interval = 30
		}
		next.FlespiInterval = interval
		next.FlespiAutostart = req.Autostart
		_ = s.SaveCfg(next)
		ident := strings.TrimSpace(next.FlespiIdent)
		script = ops.FlespiDaemonInstallScript(tok, deviceID, ident, next.FlespiHost, interval, req.Autostart)
	case "start":
		script = ops.FlespiDaemonStartScript
	case "stop":
		script = ops.FlespiDaemonStopScript
	case "status", "":
		script = ops.FlespiDaemonStatusScript
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "unbekannte Flespi-Daemon-Aktion: " + action})
		return
	}

	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 45)
	defer cancel()
	out, stderr, err := run.RunScript(ctx, script)
	resp := map[string]any{
		"ok":             err == nil,
		"raw":            strings.TrimSpace(out),
		"stderr":         strings.TrimSpace(stderr),
		"error":          fmtErr(err),
		"device_created": created,
		"device_preview": preview,
		"transport":      cfg.Transport,
	}
	if action == "status" || action == "" {
		for k, v := range parseFlespiDaemonRaw(out) {
			if _, exists := resp[k]; !exists {
				resp[k] = v
			}
		}
	}
	writeJSON(w, resp)
}
