package flespi

import (
	"encoding/json"
	"testing"
)

func TestHTTPDeviceTypeIDFromRaw(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"id":            171,
		"name":          "generic",
		"protocol_name": "http",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := httpDeviceTypeIDFromRaw(raw); got != 171 {
		t.Fatalf("httpDeviceTypeIDFromRaw() = %d, want 171", got)
	}
}

