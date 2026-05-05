package ops

import "testing"

func TestShQuoteSingle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"foo", "'foo'"},
		{"it's", "'" + "it" + `'"'"'` + "s" + "'"},
	}
	for _, c := range cases {
		got := shQuoteSingle(c.in)
		if got != c.want {
			t.Errorf("shQuoteSingle(%q) = %q want %q", c.in, got, c.want)
		}
	}
}
