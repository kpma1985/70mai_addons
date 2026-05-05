package api

import "testing"

func TestValidateAPInput(t *testing.T) {
	cases := []struct {
		name    string
		ssid    string
		pwd     string
		wantErr bool
	}{
		{name: "open ap", ssid: "70mai_X800", pwd: "", wantErr: false},
		{name: "valid wpa", ssid: "70mai_X800", pwd: "12345678", wantErr: false},
		{name: "missing ssid", ssid: "", pwd: "", wantErr: true},
		{name: "short password", ssid: "70mai_X800", pwd: "short", wantErr: true},
		{name: "long password", ssid: "70mai_X800", pwd: "1234567890123456789012345678901234567890123456789012345678901234", wantErr: true},
		{name: "long ssid", ssid: "123456789012345678901234567890123", pwd: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotErr := validateAPInput(tc.ssid, tc.pwd) != ""
			if gotErr != tc.wantErr {
				t.Fatalf("validateAPInput(%q, %q) error=%v want %v", tc.ssid, tc.pwd, gotErr, tc.wantErr)
			}
		})
	}
}
