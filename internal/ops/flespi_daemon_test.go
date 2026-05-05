package ops

import (
	"strings"
	"testing"
)

func TestFlespiDaemonInstallScript_TieredPayload(t *testing.T) {
	s := FlespiDaemonInstallScript("tok", "12345", "x800-cam", "", 30, false)
	if !strings.Contains(s, "for tier in 1 2 3") {
		t.Fatal("expected tiered send loop")
	}
	if !strings.Contains(s, "FULL_INNER") || !strings.Contains(s, "MID_INNER") || !strings.Contains(s, "MIN_INNER") {
		t.Fatal("expected tier payload variables")
	}
	if !strings.Contains(s, "FLESPI_IDENT") {
		t.Fatal("expected FLESPI_IDENT in conf")
	}
	if !strings.Contains(s, "FLESPI_HOST='https://flespi.io'") {
		t.Fatal("expected FLESPI_HOST in conf")
	}
	if !strings.Contains(s, "ENDPOINT_BASE=${FLESPI_HOST:-https://flespi.io}") {
		t.Fatal("expected ENDPOINT_BASE from FLESPI_HOST")
	}
	if !strings.Contains(s, "memory.free") {
		t.Fatal("expected memory.free in metrics tier")
	}
	if !strings.Contains(s, "filesystem.sd.available") {
		t.Fatal("expected filesystem.sd.available (SD free KB on /mnt/sd)")
	}
	// Same shape as flespi HTTP channel: JSON array of one message object, Content-Type set.
	if !strings.Contains(s, "printf '%s' '[{'") || !strings.Contains(s, "printf '%s' '}]'") {
		t.Fatal("expected JSON array written as [{ ... }] via printf")
	}
	if !strings.Contains(s, "json_esc_str") || !strings.Contains(s, "json_uint") {
		t.Fatal("expected JSON helper functions for safe payload")
	}
	if !strings.Contains(s, "Content-Type: application/json") {
		t.Fatal("expected application/json header on POST")
	}
}
