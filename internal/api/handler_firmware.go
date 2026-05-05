package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"addon_installer/internal/remote"
)

const firmwareRemotePath = "/mnt/sd/FW98530A.bin"
const baseFirmwareSize = 141221120

func (s *Server) handleFirmwareUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(180 << 20); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cfg, err := readConnFromJSONString(r.FormValue("conn"), s.CurrentConfig())
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	method := strings.ToLower(strings.TrimSpace(r.FormValue("method")))
	if method == "" {
		method = "ssh"
	}
	reboot := strings.EqualFold(r.FormValue("reboot"), "true") || r.FormValue("reboot") == "1"

	fh, hdr, err := r.FormFile("firmware")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, 170<<20))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if len(data) == 0 {
		writeJSON(w, map[string]any{"ok": false, "error": "Firmware-Datei ist leer"})
		return
	}
	if len(data) >= 170<<20 {
		writeJSON(w, map[string]any{"ok": false, "error": "Firmware-Datei zu groß"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(hdr.Filename), ".bin") {
		writeJSON(w, map[string]any{"ok": false, "error": "Firmware muss eine .bin-Datei sein"})
		return
	}

	ctx, cancel := withTimeout(r, 180)
	defer cancel()
	var uploadErr error
	var ftpCheck map[string]any
	switch method {
	case "ssh":
		if !remote.IsSSHTransport(cfg.Transport) {
			cfg.Transport = "ssh"
		}
		uploadErr = newRunner(cfg).UploadBytes(ctx, firmwareRemotePath, data, false)
	case "ftp":
		ftpCheck, uploadErr = ftpStoreFirmware(cfg.Host, cfg.Password, data)
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "Methode muss ssh oder ftp sein"})
		return
	}
	if uploadErr != nil {
		writeJSON(w, map[string]any{"ok": false, "error": uploadErr.Error(), "method": method})
		return
	}

	postRaw := ""
	postErr := error(nil)
	var sdCheck map[string]any
	if method == "ssh" {
		checkOut, checkStderr, checkErr := newRunner(cfg).Run(ctx, firmwareSDCheckScript(len(data)))
		if strings.TrimSpace(checkOut) != "" {
			_ = json.Unmarshal([]byte(strings.TrimSpace(checkOut)), &sdCheck)
		}
		if sdCheck == nil {
			sdCheck = map[string]any{"ok": false, "error": fmtErr(checkErr), "stderr": strings.TrimSpace(checkStderr), "raw": strings.TrimSpace(checkOut)}
		}
	} else {
		sdCheck = ftpCheck
	}
	sdErrs := sdCheckErrorText(sdCheck)
	rebootRequested := reboot
	if sdErrs != "" {
		reboot = false
		postErr = fmt.Errorf("SD-Check fehlgeschlagen: %s", sdErrs)
	}
	if postErr == nil && (method == "ssh" || reboot) {
		cmd := "sync"
		if reboot {
			cmd += "; (sleep 2 && /sbin/reboot) >/dev/null 2>&1 &"
		}
		out, stderr, err := newRunner(cfg).Run(ctx, cmd)
		postRaw = strings.TrimSpace(out)
		if strings.TrimSpace(stderr) != "" {
			postRaw = strings.TrimSpace(postRaw + "\n" + stderr)
		}
		postErr = err
	}

	warn := ""
	if len(data) != baseFirmwareSize {
		warn = fmt.Sprintf("Dateigröße %d weicht von Base-Referenz %d ab", len(data), baseFirmwareSize)
	}
	resp := map[string]any{
		"ok":               postErr == nil,
		"uploaded":         true,
		"method":           method,
		"path":             firmwareRemotePath,
		"bytes":            len(data),
		"source_name":      hdr.Filename,
		"reboot":           reboot,
		"reboot_requested": rebootRequested,
		"warning":          warn,
		"sd_check":         sdCheck,
		"raw":              postRaw,
		"error":            fmtErr(postErr),
	}
	writeJSON(w, resp)
}

func firmwareSDCheckScript(expected int) string {
	return fmt.Sprintf(`#!/bin/sh
json_escape() { printf '%%s' "$1" | tr -d '\r\n' | sed 's/\\/\\\\/g;s/"/\\"/g'; }
EXPECTED=%d
TARGET=/mnt/sd/FW98530A.bin
OK=true
ERRORS=""
WARNINGS=""
add_error() { OK=false; ERRORS="${ERRORS}${ERRORS:+|}$1"; }
add_warn() { WARNINGS="${WARNINGS}${WARNINGS:+|}$1"; }

[ -f "$TARGET" ] || add_error "FW98530A.bin fehlt im SD-Root"
SIZE=0
[ -f "$TARGET" ] && SIZE=$(wc -c < "$TARGET" 2>/dev/null | tr -d ' ')
[ "$SIZE" = "$EXPECTED" ] || add_error "FW98530A.bin Größe ${SIZE:-0} != Upload $EXPECTED"
[ "$SIZE" = "%d" ] || add_warn "FW98530A.bin Größe ${SIZE:-0} != Base-Referenz %d"

APPLE=$(find /mnt/sd -maxdepth 1 -name '._*' -print 2>/dev/null | sed 's#^/mnt/sd/##' | tr '\n' '|')
[ -z "$APPLE" ] || add_error "AppleDouble-Dateien im SD-Root: $APPLE"

EXTRA=$(find /mnt/sd -maxdepth 1 -type f \( -iname 'FW98530A*.bin' -o -iname '*.bin' \) ! -name 'FW98530A.bin' -print 2>/dev/null | sed 's#^/mnt/sd/##' | tr '\n' '|')
[ -z "$EXTRA" ] || add_error "Zusätzliche .bin-Dateien im SD-Root: $EXTRA"

APPLIED=$(find /mnt/sd -maxdepth 1 -type f -name 'FW98530A.bin.applied-*' -print 2>/dev/null | sed 's#^/mnt/sd/##' | tr '\n' '|')
[ -z "$APPLIED" ] || add_warn "Alte applied-Dateien vorhanden: $APPLIED"

FREE=$(df -k /mnt/sd 2>/dev/null | awk 'NR==2{print $4}')
sync
printf '{"ok":%%s,"target_present":%%s,"target_size":%%s,"expected_size":%%s,"reference_size":%%s,"free_kb":"%%s","errors":"%%s","warnings":"%%s","appledouble":"%%s","extra_bins":"%%s","applied":"%%s"}\n' \
  "$OK" "$([ -f "$TARGET" ] && echo true || echo false)" "${SIZE:-0}" "$EXPECTED" "%d" "$(json_escape "$FREE")" \
  "$(json_escape "$ERRORS")" "$(json_escape "$WARNINGS")" "$(json_escape "$APPLE")" "$(json_escape "$EXTRA")" "$(json_escape "$APPLIED")"
`, expected, baseFirmwareSize, baseFirmwareSize, baseFirmwareSize)
}

func ftpStoreFirmware(host, password string, data []byte) (map[string]any, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("FTP-Host fehlt")
	}
	addr := net.JoinHostPort(host, "21")
	conn, err := net.DialTimeout("tcp", addr, 25*time.Second)
	if err != nil {
		return nil, fmt.Errorf("ftp dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(180 * time.Second))
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if _, _, err := ftpRead(rw); err != nil {
		return nil, err
	}
	code, _, err := ftpSend(rw, "USER root")
	if err != nil {
		return nil, err
	}
	if code == 331 {
		if err := ftpCmd(rw, 230, "PASS "+password); err != nil {
			return nil, err
		}
	} else if code != 230 {
		return nil, fmt.Errorf("ftp USER: unexpected %d", code)
	}
	if err := ftpCmd(rw, 200, "TYPE I"); err != nil {
		return nil, err
	}
	if err := ftpStore(rw, host, "FW98530A.bin", data); err != nil {
		return nil, err
	}
	check := ftpSDCheck(rw, host, len(data))
	_ = ftpCmdExpectAny(rw, []int{221, 226, 250}, "QUIT")
	return check, nil
}

func ftpStore(rw *bufio.ReadWriter, host, name string, data []byte) error {
	pasvLine, err := ftpCmdLine(rw, 227, "PASV")
	if err != nil {
		return err
	}
	dataAddr, err := parsePASV(pasvLine, host)
	if err != nil {
		return err
	}
	dataConn, err := net.DialTimeout("tcp", dataAddr, 25*time.Second)
	if err != nil {
		return fmt.Errorf("ftp data dial: %w", err)
	}
	if err := ftpCmdExpectAny(rw, []int{125, 150}, "STOR "+name); err != nil {
		_ = dataConn.Close()
		return err
	}
	if _, err := io.Copy(dataConn, bytes.NewReader(data)); err != nil {
		_ = dataConn.Close()
		return fmt.Errorf("ftp data write: %w", err)
	}
	if err := dataConn.Close(); err != nil {
		return err
	}
	_, _, err = ftpRead(rw)
	return err
}

func ftpSDCheck(rw *bufio.ReadWriter, host string, expected int) map[string]any {
	size := ftpSize(rw, "FW98530A.bin")
	names, _ := ftpNLST(rw, host)
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	errors := []string{}
	warnings := []string{}
	if !nameSet["FW98530A.bin"] {
		errors = append(errors, "FW98530A.bin fehlt im SD-Root")
	}
	if size >= 0 && size != expected {
		errors = append(errors, fmt.Sprintf("FW98530A.bin Größe %d != Upload %d", size, expected))
	}
	if size >= 0 && size != baseFirmwareSize {
		warnings = append(warnings, fmt.Sprintf("FW98530A.bin Größe %d != Base-Referenz %d", size, baseFirmwareSize))
	}
	apple := []string{}
	extraBins := []string{}
	applied := []string{}
	for _, n := range names {
		l := strings.ToLower(n)
		if strings.HasPrefix(n, "._") {
			apple = append(apple, n)
		}
		if n != "FW98530A.bin" && (strings.HasSuffix(l, ".bin") || strings.HasPrefix(n, "FW98530A")) {
			extraBins = append(extraBins, n)
		}
		if strings.HasPrefix(n, "FW98530A.bin.applied-") {
			applied = append(applied, n)
		}
	}
	if len(apple) > 0 {
		errors = append(errors, "AppleDouble-Dateien im SD-Root: "+strings.Join(apple, "|"))
	}
	if len(extraBins) > 0 {
		errors = append(errors, "Zusätzliche .bin-Dateien im SD-Root: "+strings.Join(extraBins, "|"))
	}
	if len(applied) > 0 {
		warnings = append(warnings, "Alte applied-Dateien vorhanden: "+strings.Join(applied, "|"))
	}
	return map[string]any{
		"ok":             len(errors) == 0,
		"target_present": nameSet["FW98530A.bin"],
		"target_size":    size,
		"expected_size":  expected,
		"reference_size": baseFirmwareSize,
		"errors":         strings.Join(errors, "|"),
		"warnings":       strings.Join(warnings, "|"),
		"appledouble":    strings.Join(apple, "|"),
		"extra_bins":     strings.Join(extraBins, "|"),
		"applied":        strings.Join(applied, "|"),
	}
}

func sdCheckErrorText(sdCheck map[string]any) string {
	if sdCheck == nil {
		return "kein SD-Check-Ergebnis"
	}
	if v, ok := sdCheck["errors"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if ok, _ := sdCheck["ok"].(bool); !ok {
		if v, ok := sdCheck["error"].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return "SD-Check nicht OK"
	}
	return ""
}

func ftpCmd(rw *bufio.ReadWriter, expect int, cmd string) error {
	return ftpCmdExpectAny(rw, []int{expect}, cmd)
}

func ftpSend(rw *bufio.ReadWriter, cmd string) (int, string, error) {
	if _, err := rw.WriteString(cmd + "\r\n"); err != nil {
		return 0, "", err
	}
	if err := rw.Flush(); err != nil {
		return 0, "", err
	}
	return ftpRead(rw)
}

func ftpCmdLine(rw *bufio.ReadWriter, expect int, cmd string) (string, error) {
	code, line, err := ftpSend(rw, cmd)
	if err != nil {
		return line, err
	}
	if code != expect {
		return line, fmt.Errorf("ftp %s: expected %d got %d: %s", cmd, expect, code, line)
	}
	return line, nil
}

func ftpCmdExpectAny(rw *bufio.ReadWriter, expects []int, cmd string) error {
	line, err := ftpCmdLine(rw, expects[0], cmd)
	if err == nil {
		return nil
	}
	code, _, parseErr := parseFTPCode(line)
	if parseErr == nil {
		for _, e := range expects {
			if code == e {
				return nil
			}
		}
	}
	return err
}

func ftpSize(rw *bufio.ReadWriter, name string) int {
	code, line, err := ftpSend(rw, "SIZE "+name)
	if err != nil || code != 213 {
		return -1
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return -1
	}
	size, err := strconv.Atoi(fields[1])
	if err != nil {
		return -1
	}
	return size
}

func ftpNLST(rw *bufio.ReadWriter, host string) ([]string, error) {
	pasvLine, err := ftpCmdLine(rw, 227, "PASV")
	if err != nil {
		return nil, err
	}
	dataAddr, err := parsePASV(pasvLine, host)
	if err != nil {
		return nil, err
	}
	dataConn, err := net.DialTimeout("tcp", dataAddr, 25*time.Second)
	if err != nil {
		return nil, err
	}
	if err := ftpCmdExpectAny(rw, []int{125, 150}, "NLST"); err != nil {
		_ = dataConn.Close()
		return nil, err
	}
	data, readErr := io.ReadAll(dataConn)
	_ = dataConn.Close()
	_, _, doneErr := ftpRead(rw)
	if readErr != nil {
		return nil, readErr
	}
	if doneErr != nil {
		return nil, doneErr
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := []string{}
	for _, line := range lines {
		name := strings.TrimSpace(line)
		name = strings.TrimPrefix(name, "/")
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

func ftpRead(rw *bufio.ReadWriter) (int, string, error) {
	line, err := rw.ReadString('\n')
	if err != nil {
		return 0, line, err
	}
	code, multiline, err := parseFTPCode(line)
	if err != nil {
		return 0, line, err
	}
	last := strings.TrimSpace(line)
	if multiline {
		prefix := strconv.Itoa(code) + " "
		for {
			line, err = rw.ReadString('\n')
			if err != nil {
				return code, last, err
			}
			last = strings.TrimSpace(line)
			if strings.HasPrefix(line, prefix) {
				break
			}
		}
	}
	return code, last, nil
}

func parseFTPCode(line string) (int, bool, error) {
	if len(line) < 3 {
		return 0, false, fmt.Errorf("ftp response zu kurz: %q", line)
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0, false, err
	}
	return code, len(line) > 3 && line[3] == '-', nil
}

func parsePASV(line, fallbackHost string) (string, error) {
	start := strings.Index(line, "(")
	end := strings.Index(line, ")")
	if start < 0 || end <= start {
		return "", fmt.Errorf("PASV-Antwort nicht lesbar: %s", line)
	}
	parts := strings.Split(line[start+1:end], ",")
	if len(parts) != 6 {
		return "", fmt.Errorf("PASV-Antwort unvollständig: %s", line)
	}
	nums := make([]int, 6)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return "", err
		}
		nums[i] = n
	}
	host := fmt.Sprintf("%d.%d.%d.%d", nums[0], nums[1], nums[2], nums[3])
	if host == "0.0.0.0" {
		host = fallbackHost
	}
	port := nums[4]*256 + nums[5]
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
