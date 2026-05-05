package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Path returns default config file path (~/.config/x800_addon/installer.json).
func Path() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".config", "x800_addon", "installer.json")
}

type Store struct {
	Path string
}

func (s Store) Load() (Config, error) {
	var c Config
	p := s.Path
	if p == "" {
		p = Path()
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

func (s Store) Save(c Config) error {
	p := s.Path
	if p == "" {
		p = Path()
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0600)
}

// Transport: ssh | telnet
type Config struct {
	Host                  string `json:"host"`
	Transport             string `json:"transport"` // ssh (default), telnet
	SSHPort               int    `json:"ssh_port"`
	TelnetPort            int    `json:"telnet_port"`
	Password              string `json:"password,omitempty"`
	TailscaleAuthKey      string `json:"tailscale_auth_key,omitempty"`
	TailscaleHostname     string `json:"tailscale_hostname,omitempty"`
	TailscaleRoutes       string `json:"tailscale_routes,omitempty"`        // comma-separated CIDRs, e.g. "192.168.0.0/24"
	TailscaleExitNode     bool   `json:"tailscale_exit_node,omitempty"`     // --advertise-exit-node
	TailscaleAcceptRoutes bool   `json:"tailscale_accept_routes,omitempty"` // --accept-routes
	// Remove auth key from local installer.json after successful tailscale up.
	ClearTailscaleKeyAfterUp bool `json:"clear_tailscale_key_after_up,omitempty"`
	// Flespi (stored locally only; never commit to repo).
	FlespiToken string `json:"flespi_token,omitempty"`
	FlespiHost  string `json:"flespi_host,omitempty"` // e.g. https://flespi.io
	// Optional full path to iptables on the camera (aarch64/musl), e.g. /mnt/sd/bin/iptables — empty = PATH.
	IptablesPath string `json:"iptables_path,omitempty"`
	// Flespi device ID for telemetry upload (POST /gw/devices/{id}/messages).
	FlespiDeviceID string `json:"flespi_device_id,omitempty"`
	// Optional fallback Flespi device-type ID if the HTTP type cannot be found automatically in the account.
	FlespiDeviceTypeID string `json:"flespi_device_type_id,omitempty"`
	// Optional stable ident for auto-created Flespi HTTP devices.
	FlespiIdent string `json:"flespi_ident,omitempty"`
	// Flespi daemon upload interval in seconds.
	FlespiInterval int `json:"flespi_interval,omitempty"`
	// Start Flespi daemon at boot via main_app wrapper.
	FlespiAutostart bool `json:"flespi_autostart,omitempty"`
	// Automatically connect on startup when host is set.
	AutoConnect bool `json:"auto_connect,omitempty"`
}

func Default() Config {
	return Config{
		Host:              "192.168.0.1",
		Transport:         "ssh",
		SSHPort:           22,
		TelnetPort:        23,
		Password:          "",
		TailscaleHostname: "70mai-x800",
		FlespiHost:        "https://flespi.io",
		FlespiInterval:    30,
	}
}

// StripFlespi clears all Flespi-related fields and restores API host / interval defaults.
func StripFlespi(c Config) Config {
	d := Default()
	c.FlespiToken = ""
	c.FlespiHost = d.FlespiHost
	c.FlespiDeviceID = ""
	c.FlespiDeviceTypeID = ""
	c.FlespiIdent = ""
	c.FlespiInterval = d.FlespiInterval
	c.FlespiAutostart = false
	return c
}
