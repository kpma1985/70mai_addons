package api

import (
	"net/http"
	"strings"
	"sync"

	"addon_installer/internal/localtailscale"
	"addon_installer/internal/ops"
	"addon_installer/internal/remote"
)

func (s *Server) handleLocalTailscale(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := withTimeout(r, 15)
	defer cancel()
	writeJSON(w, localtailscale.QueryStatus(ctx))
}

// handleOverview bundles camera status + Tailscale status in parallel — one request for the UI status line.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := withTimeout(r, 35)
	defer cancel()

	var stRaw, tsRaw, uptimeRaw, stStderr, tsStderr, statusSource string
	var stErr, tsErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		stRaw, stStderr, stErr = fetchCameraStatus(ctx, cfg)
		statusSource = cfg.Transport
	}()
	go func() {
		defer wg.Done()
		tsRaw, tsStderr, tsErr = fetchCameraTailscaleStatus(ctx, cfg)
	}()
	go func() {
		defer wg.Done()
		if !strings.EqualFold(cfg.Transport, "http") && !strings.EqualFold(cfg.Transport, "telnet") {
			run := &remote.Runner{Cfg: cfg}
			out, _, _ := run.Run(ctx, "cat /proc/uptime")
			uptimeRaw = strings.TrimSpace(out)
		}
	}()
	wg.Wait()

	stErrStr := ""
	if stErr != nil {
		stErrStr = stErr.Error()
	}
	if strings.TrimSpace(stStderr) != "" {
		if stErrStr != "" {
			stErrStr += " · " + stStderr
		} else {
			stErrStr = stStderr
		}
	}
	tsErrStr := ""
	if tsErr != nil {
		tsErrStr = tsErr.Error()
	}
	if strings.TrimSpace(tsStderr) != "" {
		if tsErrStr != "" {
			tsErrStr += " · " + tsStderr
		} else {
			tsErrStr = tsStderr
		}
	}

	wlan := parseJSONObject(stRaw)
	tsCam := parseJSONObject(tsRaw)
	errs := map[string]any{}
	if stErrStr != "" {
		errs["status"] = stErrStr
	}
	if tsErrStr != "" {
		errs["tailscale"] = tsErrStr
	}
	writeJSON(w, map[string]any{
		"ok":                stErr == nil,
		"status_ok":         stErr == nil,
		"tailscale_ok":      tsErr == nil,
		"camera_transport":  cfg.Transport,
		"status_source":     statusSource,
		"camera_host":       cfg.Host,
		"wlan":              wlan,
		"ap_summary":        summarizeWLAN(wlan),
		"tailscale_cam":     tsCam,
		"tailscale_summary": summarizeTailscaleCam(tsCam, tsRaw),
		"uptime_raw":        uptimeRaw,
		"errors":            errs,
		"raw": map[string]any{
			"status":    stRaw,
			"tailscale": tsRaw,
		},
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := withTimeout(r, 30)
	defer cancel()

	out, stderr, err := fetchCameraStatus(ctx, cfg)
	resp := map[string]any{"raw": out, "stderr": stderr}
	if err != nil {
		resp["ok"] = false
		resp["error"] = err.Error()
		if strings.EqualFold(cfg.Transport, "telnet") {
			resp["hint"] = "telnet nur Einzeiler — ssh bevorzugt"
		}
	} else {
		resp["ok"] = true
	}
	writeJSON(w, resp)
}

// handleGPS extracts GPS fields live from gps_state.txt/serial TTYs when SSH is
// available, otherwise it falls back to the generic camera status script.
func (s *Server) handleGPS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := readConn(r, s.CurrentConfig())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := withTimeout(r, 30)
	defer cancel()

	if remote.IsSSHTransport(cfg.Transport) {
		run := newRunner(cfg)
		liveOut, liveStderr, liveErr := run.RunScript(ctx, ops.GPSLiveScript)
		live := parseJSONObject(liveOut)
		if len(live) > 0 {
			live["stderr"] = strings.TrimSpace(liveStderr)
			live["error"] = fmtErr(liveErr)
			live["transport"] = cfg.Transport
			live["hint"] = "Live-GPS: zuerst /mnt/sd/gps_state.txt, danach CASIC /dev/ttyS1@115200 mit NMEA RMC/GGA. /dev/ttyUSB6 ist Quectel-binär und wird nicht als NMEA geparst."
			if _, ok := live["ok"]; !ok {
				live["ok"] = liveErr == nil
			}
			writeJSON(w, live)
			return
		}
	}

	out, stderr, err := fetchCameraStatus(ctx, cfg)
	m := parseJSONObject(out)
	lat, _ := m["gps_lat"].(string)
	lon, _ := m["gps_lon"].(string)
	tm, _ := m["gps_time"].(string)
	gpsOK := false
	if v, ok := m["gps_available"].(bool); ok {
		gpsOK = v
	}
	resp := map[string]any{
		"ok":            err == nil,
		"gps_lat":       strings.TrimSpace(lat),
		"gps_lon":       strings.TrimSpace(lon),
		"gps_time":      strings.TrimSpace(tm),
		"gps_available": gpsOK,
		"raw":           out,
		"stderr":        strings.TrimSpace(stderr),
		"error":         fmtErr(err),
		"source":        "status-script",
		"hint":          "Fallback: gps_* kommen aus /mnt/sd/gps_state.txt über den Status-Script. Für Live-TTY-GPS bitte SSH nutzen.",
	}
	writeJSON(w, resp)
}

func (s *Server) handleGPSDebug(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{
			"ok":    false,
			"error": "GPS-Debug braucht SSH, weil serielle Geräte direkt gelesen werden.",
		})
		return
	}
	ctx, cancel := withTimeout(r, 20)
	defer cancel()
	run := newRunner(cfg)
	out, stderr, err := run.RunScript(ctx, ops.GPSDebugScript)
	resp := parseJSONObject(out)
	if len(resp) == 0 {
		resp = map[string]any{"raw_debug": strings.TrimSpace(out)}
	}
	resp["ok"] = err == nil
	resp["stderr"] = strings.TrimSpace(stderr)
	resp["error"] = fmtErr(err)
	resp["transport"] = cfg.Transport
	writeJSON(w, resp)
}

// handleLiveHostapd runs live SSH single-commands on the camera (no RunScript/Heredoc) — ls, pgrep, head.
func (s *Server) handleLiveHostapd(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, map[string]any{
			"ok":    false,
			"error": "Live-Abfrage nur mit Transport 'ssh' (direkt Shell-Befehle auf der Kamera).",
		})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 25)
	defer cancel()

	lsOut, lsStderr, lsErr := run.Run(ctx, "ls -la /etc/wifiap_wpa2.conf /var/run/hostapd.conf 2>&1")
	pgOut, pgStderr, pgErr := run.Run(ctx, "pgrep -fl hostapd 2>&1")
	if strings.TrimSpace(pgOut) == "" && pgErr == nil {
		pgOut, pgStderr, pgErr = run.Run(ctx, `ps 2>&1 | grep '[h]ostapd' | head -8`)
	}
	runOut, runStderr, runErr := run.Run(ctx, "head -n 60 /var/run/hostapd.conf 2>&1")
	srcSSID, _, srcErr := run.Run(ctx, `sh -c 'S=$(grep "^ssid=" /var/run/hostapd.conf 2>/dev/null | head -1 | cut -d= -f2-); [ -n "$S" ] || S=$(grep "^ssid=" /etc/wifiap_wpa2.conf 2>/dev/null | head -1 | cut -d= -f2-); printf %s "$S"'`)

	writeJSON(w, map[string]any{
		"ok":                  true,
		"paths_ls":            strings.TrimSpace(lsOut),
		"paths_ls_stderr":     strings.TrimSpace(lsStderr),
		"paths_ls_error":      fmtErr(lsErr),
		"hostapd_process":     strings.TrimSpace(pgOut),
		"process_stderr":      strings.TrimSpace(pgStderr),
		"process_error":       fmtErr(pgErr),
		"runtime_conf_head":   strings.TrimSpace(runOut),
		"runtime_conf_stderr": strings.TrimSpace(runStderr),
		"runtime_conf_error":  fmtErr(runErr),
		"source_ssid":         strings.TrimSpace(srcSSID),
		"source_ssid_error":   fmtErr(srcErr),
		"hint":                "SSID nur aus Konfig-Dateien (grep), kein iw/hostapd_cli: /var/run/hostapd.conf, Fallback /etc/wifiap_wpa2.conf. Rohdaten: ssh Session.Run.",
	})
}
