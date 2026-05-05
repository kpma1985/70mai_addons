package ops

import (
	"strings"
	"testing"
)

func TestFlespiInstallScript(t *testing.T) {
	s := FlespiInstallScript("")
	if !strings.Contains(s, "flespi-ping.sh") {
		t.Fatalf("expected ping script path")
	}
	s2 := FlespiInstallScript("abc'def")
	if !strings.Contains(s2, "flespi.conf") {
		t.Fatalf("expected conf write")
	}
}
