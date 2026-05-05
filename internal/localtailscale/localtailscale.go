package localtailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Peer is one machine in the tailnet (including optional exit/subnet hints).
type Peer struct {
	Label        string   `json:"label"`
	Host         string   `json:"host"`
	DNSName      string   `json:"dns_name,omitempty"`
	Online       bool     `json:"online"`
	ExitNode     bool     `json:"exit_node,omitempty"`
	ExitOption   bool     `json:"exit_node_option,omitempty"`
	SubnetRoutes []string `json:"subnet_routes,omitempty"`
	Self         bool     `json:"self,omitempty"`
}

// LocalStatus is returned for GET /api/local/tailscale (runs on the PC that hosts the installer).
type LocalStatus struct {
	OK          bool    `json:"ok"`
	Running     bool    `json:"running"`
	Error       string  `json:"error,omitempty"`
	Hint        string  `json:"hint,omitempty"`
	LocalDNS    string  `json:"local_dns,omitempty"`
	LocalIPs    []string `json:"local_ips,omitempty"`
	Version     string  `json:"version,omitempty"`
	Peers       []Peer  `json:"peers"`
}

type tsDoc struct {
	Version string          `json:"Version"`
	Self    tsSelf          `json:"Self"`
	Peer    map[string]tsPeer `json:"Peer"`
}

type tsSelf struct {
	DNSName       string   `json:"DNSName"`
	HostName      string   `json:"HostName"`
	TailscaleIPs  []string `json:"TailscaleIPs"`
}

type tsPeer struct {
	DNSName        string   `json:"DNSName"`
	HostName       string   `json:"HostName"`
	TailscaleIPs   []string `json:"TailscaleIPs"`
	Online         bool     `json:"Online"`
	ExitNode       bool     `json:"ExitNode"`
	ExitNodeOption bool     `json:"ExitNodeOption"`
	PrimaryRoutes  []string `json:"PrimaryRoutes"`
}

// QueryStatus runs `tailscale status --json` on the local machine (installer host).
func QueryStatus(ctx context.Context) LocalStatus {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	var outB, errB bytes.Buffer
	cmd.Stdout = &outB
	cmd.Stderr = &errB
	err := cmd.Run()
	stderr := strings.TrimSpace(errB.String())

	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return LocalStatus{OK: true, Running: false, Hint: "tailscale-CLI nicht im PATH", Peers: nil}
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && stderr != "" {
			return LocalStatus{
				OK:      true,
				Running: false,
				Error:   firstLine(stderr),
				Hint:    "lokalen Tailscale-Client starten oder einloggen",
				Peers:   nil,
			}
		}
		return LocalStatus{
			OK:      true,
			Running: false,
			Error:   err.Error(),
			Peers:   nil,
		}
	}

	var doc tsDoc
	if err := json.Unmarshal(outB.Bytes(), &doc); err != nil {
		return LocalStatus{OK: false, Error: fmt.Sprintf("json: %v", err), Peers: nil}
	}

	out := LocalStatus{
		OK:       true,
		Running:  true,
		Version:  strings.TrimSpace(doc.Version),
		LocalDNS: strings.TrimSpace(doc.Self.DNSName),
		LocalIPs: append([]string(nil), doc.Self.TailscaleIPs...),
		Peers:    nil,
	}

	selfHost := pickHost(doc.Self.TailscaleIPs, doc.Self.DNSName)
	selfLabel := labelFor(doc.Self.HostName, doc.Self.DNSName, doc.Self.TailscaleIPs, false, false, nil)
	out.Peers = append(out.Peers, Peer{
		Label:   selfLabel + " (dieses Gerät)",
		Host:    selfHost,
		DNSName: doc.Self.DNSName,
		Online:  true,
		Self:    true,
	})

	keys := make([]string, 0, len(doc.Peer))
	for k := range doc.Peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		p := doc.Peer[k]
		if strings.TrimSpace(p.DNSName) == "" && strings.TrimSpace(p.HostName) == "" {
			continue
		}
		h := pickHost(p.TailscaleIPs, p.DNSName)
		if h == "" {
			continue
		}
		routes := append([]string(nil), p.PrimaryRoutes...)
		lbl := labelFor(p.HostName, p.DNSName, p.TailscaleIPs, p.ExitNode, p.ExitNodeOption, routes)
		if !p.Online {
			lbl += " · offline"
		}
		out.Peers = append(out.Peers, Peer{
			Label:        lbl,
			Host:         h,
			DNSName:      p.DNSName,
			Online:       p.Online,
			ExitNode:     p.ExitNode,
			ExitOption:   p.ExitNodeOption,
			SubnetRoutes: routes,
		})
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func pickHost(ips []string, dns string) string {
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip != "" && strings.HasPrefix(ip, "100.") {
			return ip
		}
	}
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			return ip
		}
	}
	d := strings.TrimSuffix(strings.TrimSpace(dns), ".")
	if d != "" {
		return d
	}
	return ""
}

func labelFor(hostName, dnsName string, ips []string, exitNode, exitOpt bool, routes []string) string {
	h := strings.TrimSpace(hostName)
	if h == "" {
		h = strings.TrimSuffix(strings.TrimSpace(dnsName), ".")
	}
	if h == "" {
		h = "peer"
	}
	var tsIP string
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if strings.HasPrefix(ip, "100.") {
			tsIP = ip
			break
		}
	}
	if tsIP == "" && len(ips) > 0 {
		tsIP = strings.TrimSpace(ips[0])
	}
	parts := []string{h}
	if tsIP != "" {
		parts = append(parts, tsIP)
	}
	if exitNode {
		parts = append(parts, "Exit aktiv")
	} else if exitOpt {
		parts = append(parts, "Exit angeboten")
	}
	if len(routes) > 0 {
		parts = append(parts, "Subnet: "+strings.Join(routes, ", "))
	}
	return strings.Join(parts, " · ")
}
