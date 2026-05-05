package api

import (
	"embed"
	"io/fs"
	"net/http"

	"addon_installer/internal/config"
	"addon_installer/internal/version"
)

// Server holds all shared state needed by the HTTP handlers.
type Server struct {
	CurrentConfig func() config.Config
	SaveCfg       func(config.Config) error
	Store         *config.Store
	// WebFS is the embed.FS containing the web/ directory (from package main).
	WebFS embed.FS
	// WpalibFS is the embed.FS containing the bin/wpalib/ directory (from package main).
	WpalibFS embed.FS
}

// RegisterRoutes mounts all API and static-file routes onto mux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Static files (web/*)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		fsys, _ := fs.Sub(s.WebFS, "web")
		http.FileServer(http.FS(fsys)).ServeHTTP(w, r)
	})

	// Config
	mux.HandleFunc("/api/version", s.handleVersion)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/test", s.handleTest)

	// Status
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/gps", s.handleGPS)
	mux.HandleFunc("/api/gps/debug", s.handleGPSDebug)
	mux.HandleFunc("/api/live/hostapd", s.handleLiveHostapd)
	mux.HandleFunc("/api/local/tailscale", s.handleLocalTailscale)

	// System
	mux.HandleFunc("/api/system/status", s.handleSystemStatus)
	mux.HandleFunc("/api/system/reboot", s.handleSystemReboot)

	// Files
	mux.HandleFunc("/api/files/list", s.handleFilesList)
	mux.HandleFunc("/api/files/read", s.handleFilesRead)
	mux.HandleFunc("/api/files/upload", s.handleFilesUpload)

	// Firmware
	mux.HandleFunc("/api/firmware/upload", s.handleFirmwareUpload)

	// AP
	mux.HandleFunc("/api/ap", s.handleAP)
	mux.HandleFunc("/api/ap/forwarding", s.handleAPForwarding)

	// WiFi
	mux.HandleFunc("/api/wifi/status", s.handleWifiStatus)
	mux.HandleFunc("/api/wifi/wrapper", s.handleWifiWrapper)
	mux.HandleFunc("/api/wifi/connect", s.handleWifiConnect)
	mux.HandleFunc("/api/wifi/disconnect", s.handleWifiDisconnect)
	mux.HandleFunc("/api/wifi/health", s.handleWifiHealth)
	mux.HandleFunc("/api/wifi/scan", s.handleWifiScan)
	mux.HandleFunc("/api/wifi/vendor-check", s.handleWifiVendorCheck)
	mux.HandleFunc("/api/wifi/upload-wpalib", s.handleWifiUploadWpalib)

	// Tailscale
	mux.HandleFunc("/api/tailscale/download", s.handleTailscaleDownload)
	mux.HandleFunc("/api/tailscale/start", s.handleTailscaleStart)
	mux.HandleFunc("/api/tailscale/stop", s.handleTailscaleStop)
	mux.HandleFunc("/api/tailscale/up", s.handleTailscaleUp)
	mux.HandleFunc("/api/tailscale/status", s.handleTailscaleStatus)
	mux.HandleFunc("/api/tailscale/routes", s.handleTailscaleRoutes)
	mux.HandleFunc("/api/tailscale/autostart", s.handleTailscaleAutostart)

	// Flespi
	mux.HandleFunc("/api/flespi/test", s.handleFlespiTest)
	mux.HandleFunc("/api/flespi/device", s.handleFlespiDevice)
	mux.HandleFunc("/api/flespi/deploy", s.handleFlespiDeploy)

	// Flespi Daemon
	mux.HandleFunc("/api/flespi/daemon", s.handleFlespiDaemon)
	mux.HandleFunc("/api/flespi/reset", s.handleFlespiReset)

	// Ethernet
	mux.HandleFunc("/api/ethernet", s.handleEthernet)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"name":    version.AppName,
		"version": version.Version,
		"series":  version.Series,
	})
}

// Wrap applies CORS and optional API token middleware around the mux.
func Wrap(token string, mux http.Handler) http.Handler {
	return withCORS(withAPIToken(token, mux))
}
