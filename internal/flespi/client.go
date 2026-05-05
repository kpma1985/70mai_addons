package flespi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBase = "https://flespi.io"

// VerifyToken calls the Flespi GW API with the given token (GET /gw/devices/all, limited fields).
func VerifyToken(ctx context.Context, token, base string) (statusCode int, preview string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", fmt.Errorf("flespi token fehlt")
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = defaultBase
	}
	base = strings.TrimRight(base, "/")
	u := base + "/gw/devices/all?fields=id,name&limit=5"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "FlespiToken "+token)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return resp.StatusCode, "", err
	}
	preview = strings.TrimSpace(string(body))
	if len(preview) > 800 {
		preview = preview[:800] + "…"
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, preview, fmt.Errorf("flespi http %d", resp.StatusCode)
	}
	return resp.StatusCode, preview, nil
}

type Device struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Ident  string `json:"configuration.ident"`
	TypeID int64  `json:"device_type_id"`
}

type EnsureDeviceResult struct {
	Device  Device
	Created bool
	Preview string
}

type restEnvelope struct {
	Result []json.RawMessage `json:"result"`
	Errors []any             `json:"errors"`
}

// EnsureHTTPDevice returns an existing X800 HTTP device by ident/name or creates
// one with the HTTP protocol device type. The resulting Flespi numeric device ID
// is suitable for POST /gw/devices/{id}/messages.
func EnsureHTTPDevice(ctx context.Context, token, base, name, ident, deviceTypeID string) (EnsureDeviceResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return EnsureDeviceResult{}, fmt.Errorf("flespi token fehlt")
	}
	base = normalizeBase(base)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "70mai X800"
	}
	ident = strings.TrimSpace(ident)
	if ident == "" {
		ident = "x800-addon-installer"
	}

	existing, preview, err := findDevice(ctx, token, base, name, ident)
	if err != nil {
		return EnsureDeviceResult{}, err
	}
	if existing.ID > 0 {
		return EnsureDeviceResult{Device: existing, Created: false, Preview: preview}, nil
	}

	typeID, err := parsePositiveID(deviceTypeID)
	if err != nil {
		return EnsureDeviceResult{}, fmt.Errorf("Flespi Device-Type-ID muss numerisch sein")
	}
	typePreview := ""
	if typeID == 0 {
		var lookupErr error
		typeID, typePreview, lookupErr = lookupHTTPDeviceTypeID(ctx, token, base)
		if lookupErr != nil {
			dev, createPreview, createErr := createDeviceByHints(ctx, token, base, name, ident)
			if createErr == nil {
				return EnsureDeviceResult{Device: dev, Created: true, Preview: createPreview}, nil
			}
			preview := strings.TrimSpace(typePreview)
			if createPreview != "" {
				if preview != "" {
					preview += "\n\n"
				}
				preview += createPreview
			}
			return EnsureDeviceResult{Preview: preview}, lookupErr
		}
	}
	dev, createPreview, err := createDevice(ctx, token, base, name, ident, typeID)
	if err != nil {
		return EnsureDeviceResult{Preview: createPreview}, err
	}
	return EnsureDeviceResult{Device: dev, Created: true, Preview: createPreview}, nil
}

func normalizeBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = defaultBase
	}
	return strings.TrimRight(base, "/")
}

func findDevice(ctx context.Context, token, base, name, ident string) (Device, string, error) {
	u := base + "/gw/devices/all?fields=id,name,device_type_id,configuration.ident&limit=1000"
	status, body, err := doFlespi(ctx, token, http.MethodGet, u, nil)
	if err != nil {
		return Device{}, body, err
	}
	if status < 200 || status >= 300 {
		return Device{}, body, fmt.Errorf("flespi devices lookup http %d", status)
	}
	var env restEnvelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return Device{}, body, fmt.Errorf("flespi devices lookup JSON: %w", err)
	}
	for _, raw := range env.Result {
		dev := decodeDevice(raw)
		if dev.ID <= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(dev.Ident), ident) || strings.EqualFold(strings.TrimSpace(dev.Name), name) {
			return dev, body, nil
		}
	}
	return Device{}, body, nil
}

func lookupHTTPDeviceTypeID(ctx context.Context, token, base string) (int64, string, error) {
	candidates := []string{
		base + "/gw/channel-protocols/all/device-types/all?fields=id,name,protocol_name&limit=5000",
		base + "/gw/channel-protocols/" + url.PathEscape(`protocol_name="http"`) + "/device-types/all?fields=id,name,protocol_name",
		base + "/gw/channel-protocols/" + url.PathEscape(`protocol_name=="http"`) + "/device-types/all?fields=id,name,protocol_name",
		base + "/gw/protocols/" + url.PathEscape(`protocol_name="http"`) + "/device-types/all?fields=id,name",
		base + "/gw/protocols/" + url.PathEscape(`protocol_name='http'`) + "/device-types/all?fields=id,name",
		base + "/gw/protocols/" + url.PathEscape(`name="HTTP"`) + "/device-types/all?fields=id,name",
		base + "/gw/protocols/" + url.PathEscape("http") + "/device-types/all?fields=id,name",
		base + "/gw/protocols/all/device-types/all?fields=id,name,protocol_name,protocol.name&limit=5000",
		base + "/gw/device-types/all?fields=id,name,protocol_name,protocol.name&limit=5000",
		base + "/gw/protocols/all?fields=id,name,protocol_name,device_types&limit=1000",
	}
	var lastPreview string
	for _, u := range candidates {
		status, body, err := doFlespi(ctx, token, http.MethodGet, u, nil)
		lastPreview = body
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		var env restEnvelope
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			continue
		}
		for _, raw := range env.Result {
			if id := httpDeviceTypeIDFromRaw(raw); id > 0 {
				return id, body, nil
			}
		}
		if len(env.Result) == 1 {
			var row map[string]any
			if json.Unmarshal(env.Result[0], &row) == nil {
				if id := numberID(row["id"]); id > 0 {
					return id, body, nil
				}
			}
		}
	}
	return 0, lastPreview, fmt.Errorf("HTTP device_type_id nicht automatisch gefunden; bitte Flespi Device-Type-ID optional eintragen oder HTTP-Protokoll/Gerätetyp im Account prüfen")
}

func createDevice(ctx context.Context, token, base, name, ident string, typeID int64) (Device, string, error) {
	body := []map[string]any{{
		"name":           name,
		"device_type_id": typeID,
		"configuration": map[string]any{
			"ident": ident,
		},
	}}
	b, _ := json.Marshal(body)
	status, respBody, err := doFlespi(ctx, token, http.MethodPost, base+"/gw/devices", b)
	if err != nil {
		return Device{}, respBody, err
	}
	if status < 200 || status >= 300 {
		return Device{}, respBody, fmt.Errorf("flespi device create http %d", status)
	}
	var env restEnvelope
	if err := json.Unmarshal([]byte(respBody), &env); err != nil {
		return Device{}, respBody, fmt.Errorf("flespi device create JSON: %w", err)
	}
	if len(env.Result) == 0 {
		return Device{}, respBody, fmt.Errorf("flespi device create: keine result-Daten")
	}
	dev := decodeDevice(env.Result[0])
	if dev.ID <= 0 {
		return Device{}, respBody, fmt.Errorf("flespi device create: keine numerische ID in Antwort")
	}
	if dev.Name == "" {
		dev.Name = name
	}
	if dev.Ident == "" {
		dev.Ident = ident
	}
	if dev.TypeID == 0 {
		dev.TypeID = typeID
	}
	return dev, respBody, nil
}

func createDeviceByHints(ctx context.Context, token, base, name, ident string) (Device, string, error) {
	payloads := [][]map[string]any{
		{{
			"name":             name,
			"device_type_name": "HTTP",
			"configuration":    map[string]any{"ident": ident},
		}},
		{{
			"name":          name,
			"protocol_name": "http",
			"configuration": map[string]any{"ident": ident},
		}},
		{{
			"name":          name,
			"configuration": map[string]any{"ident": ident},
		}},
	}
	var lastPreview string
	for _, payload := range payloads {
		b, _ := json.Marshal(payload)
		status, respBody, err := doFlespi(ctx, token, http.MethodPost, base+"/gw/devices", b)
		lastPreview = respBody
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		dev, parseErr := decodeCreatedDevice(respBody, name, ident, 0)
		if parseErr != nil {
			lastPreview = respBody
			continue
		}
		return dev, respBody, nil
	}
	return Device{}, lastPreview, fmt.Errorf("flespi device create ohne device_type_id fehlgeschlagen")
}

func doFlespi(ctx context.Context, token, method, u string, body []byte) (int, string, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "FlespiToken "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(data)), nil
}

func decodeDevice(raw json.RawMessage) Device {
	var row map[string]any
	if json.Unmarshal(raw, &row) != nil {
		return Device{}
	}
	ident := stringVal(row["configuration.ident"])
	if ident == "" {
		if cfg, ok := row["configuration"].(map[string]any); ok {
			ident = stringVal(cfg["ident"])
		}
	}
	return Device{
		ID:     numberID(row["id"]),
		Name:   stringVal(row["name"]),
		Ident:  ident,
		TypeID: numberID(row["device_type_id"]),
	}
}

func decodeCreatedDevice(respBody, name, ident string, typeID int64) (Device, error) {
	var env restEnvelope
	if err := json.Unmarshal([]byte(respBody), &env); err != nil {
		return Device{}, fmt.Errorf("flespi device create JSON: %w", err)
	}
	if len(env.Result) == 0 {
		return Device{}, fmt.Errorf("flespi device create: keine result-Daten")
	}
	dev := decodeDevice(env.Result[0])
	if dev.ID <= 0 {
		return Device{}, fmt.Errorf("flespi device create: keine numerische ID in Antwort")
	}
	if dev.Name == "" {
		dev.Name = name
	}
	if dev.Ident == "" {
		dev.Ident = ident
	}
	if dev.TypeID == 0 {
		dev.TypeID = typeID
	}
	return dev, nil
}

func httpDeviceTypeIDFromRaw(raw json.RawMessage) int64 {
	var row map[string]any
	if json.Unmarshal(raw, &row) != nil {
		return 0
	}
	name := strings.ToLower(stringVal(row["name"]))
	proto := strings.ToLower(firstString(row, "protocol_name", "protocol.name"))
	if proto == "" {
		if p, ok := row["protocol"].(map[string]any); ok {
			proto = strings.ToLower(firstString(p, "name", "protocol_name"))
		}
	}
	if proto == "http" || strings.Contains(name, "http") {
		if id := numberID(row["id"]); id > 0 {
			return id
		}
	}
	if types, ok := row["device_types"].([]any); ok {
		for _, item := range types {
			nested, ok := item.(map[string]any)
			if !ok {
				continue
			}
			nestedName := strings.ToLower(stringVal(nested["name"]))
			if proto == "http" || strings.Contains(nestedName, "http") || strings.Contains(name, "http") {
				if id := numberID(nested["id"]); id > 0 {
					return id
				}
			}
		}
	}
	return 0
}

func firstString(row map[string]any, keys ...string) string {
	for _, key := range keys {
		if v := stringVal(row[key]); v != "" {
			return v
		}
	}
	return ""
}

func parsePositiveID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return n, nil
}

func numberID(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		var n int64
		_, _ = fmt.Sscan(x, &n)
		return n
	default:
		return 0
	}
}

func stringVal(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
