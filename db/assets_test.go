package db

import "testing"

func TestCalcCSegment(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"192.168.1.5", "192.168.1.0/24"},
		{"10.0.0.255", "10.0.0.0/24"},
		{"", ""},
		{"notanip", ""},
	}
	for _, tc := range tests {
		got := calcCSegment(tc.ip)
		if got != tc.want {
			t.Errorf("calcCSegment(%q): want %q, got %q", tc.ip, tc.want, got)
		}
	}
}

func TestMarshalStringArray(t *testing.T) {
	got := marshalStringArray([]string{"a", "b", "c"})
	if got != `{"a","b","c"}` {
		t.Errorf("unexpected: %q", got)
	}
	got2 := marshalStringArray(nil)
	if got2 != "{}" {
		t.Errorf("empty: %q", got2)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"HTTPS://Example.COM/path", "https://example.com/path"},
		{"http://example.com/", "http://example.com"},
		{"http://example.com/page", "http://example.com/page"},
	}
	for _, tc := range tests {
		got := normalizeURL(tc.raw)
		if got != tc.want {
			t.Errorf("normalizeURL(%q): want %q, got %q", tc.raw, tc.want, got)
		}
	}
}
