package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"addon_installer/internal/remote"
)

func (s *Server) handleFilesList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req FileListRequest
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "Filebrowser braucht ssh oder telnet"})
		return
	}
	remotePath := cleanRemotePath(req.Path)
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 25)
	defer cancel()
	out, stderr, err := run.Run(ctx, "LC_ALL=C ls -la "+shellQuoteSingle(remotePath)+" 2>&1")
	if err != nil {
		writeJSON(w, map[string]any{
			"ok":     false,
			"path":   remotePath,
			"raw":    strings.TrimSpace(out),
			"stderr": strings.TrimSpace(stderr),
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"path":    remotePath,
		"parent":  parentRemotePath(remotePath),
		"entries": parseLSListing(remotePath, out),
		"raw":     strings.TrimSpace(out),
		"stderr":  strings.TrimSpace(stderr),
	})
}

func (s *Server) handleFilesRead(w http.ResponseWriter, r *http.Request) {
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
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg := applyConn(req.connBody, s.CurrentConfig())
	if strings.EqualFold(cfg.Transport, "http") {
		writeJSON(w, map[string]any{"ok": false, "error": "Datei lesen braucht ssh oder telnet"})
		return
	}
	max := req.MaxBytes
	if max <= 0 || max > 8*1024*1024 {
		max = 512 * 1024
	}
	remotePath := cleanRemotePath(req.Path)
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 45)
	defer cancel()
	checkInner := `f=` + shellQuoteSingle(remotePath) + `; if [ ! -f "$f" ]; then echo NOT_FILE; exit 2; fi; wc -c <"$f" | tr -d " "`
	sizeOut, sizeStderr, sizeErr := run.Run(ctx, "sh -c "+shellQuoteSingle(checkInner))
	sizeText := strings.TrimSpace(sizeOut)
	if sizeErr != nil || strings.HasPrefix(sizeText, "NOT_FILE") {
		writeJSON(w, map[string]any{
			"ok":     false,
			"path":   remotePath,
			"stderr": strings.TrimSpace(sizeStderr),
			"error":  fmtErr(sizeErr),
			"hint":   sizeText,
		})
		return
	}
	size, convErr := strconv.Atoi(sizeText)
	if convErr != nil {
		writeJSON(w, map[string]any{"ok": false, "path": remotePath, "error": "Dateigröße nicht lesbar", "hint": sizeText})
		return
	}
	if size > max {
		writeJSON(w, map[string]any{"ok": false, "path": remotePath, "error": "Datei zu groß", "hint": sizeText})
		return
	}
	out, stderr, err := run.Run(ctx, "cat "+shellQuoteSingle(remotePath))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "path": remotePath, "stderr": strings.TrimSpace(stderr), "error": fmtErr(err)})
		return
	}
	writeJSON(w, map[string]any{
		"ok":          true,
		"path":        remotePath,
		"data_base64": base64.StdEncoding.EncodeToString([]byte(out)),
		"stderr":      strings.TrimSpace(stderr),
	})
}

func (s *Server) handleFilesUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cfg, err := readConnFromJSONString(r.FormValue("conn"), s.CurrentConfig())
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !remote.IsSSHTransport(cfg.Transport) {
		writeJSON(w, map[string]any{"ok": false, "error": "Upload nur per ssh"})
		return
	}
	dir := cleanRemotePath(r.FormValue("target_dir"))
	if dir != "/mnt/sd" && !strings.HasPrefix(dir, "/mnt/sd/") {
		writeJSON(w, map[string]any{"ok": false, "error": "Ziel nur unter /mnt/sd erlaubt"})
		return
	}
	fh, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, 48<<20))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	baseName := path.Base(hdr.Filename)
	if baseName == "." || baseName == "/" || baseName == "" || strings.Contains(baseName, "\x00") {
		writeJSON(w, map[string]any{"ok": false, "error": "ungültiger Dateiname"})
		return
	}
	remotePath := cleanRemotePath(path.Join(dir, baseName))
	if remotePath != "/mnt/sd" && !strings.HasPrefix(remotePath, "/mnt/sd/") {
		writeJSON(w, map[string]any{"ok": false, "error": "Zielpfad ausserhalb /mnt/sd"})
		return
	}
	run := newRunner(cfg)
	ctx, cancel := withTimeout(r, 120)
	defer cancel()
	if err := run.UploadBytes(ctx, remotePath, data, false); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "path": remotePath})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": remotePath, "bytes": len(data)})
}
