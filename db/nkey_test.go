package db

import "testing"

func TestRootDomainRejectsIPHosts(t *testing.T) {
	tests := []struct {
		host     string
		wantRoot string
		wantApex bool
	}{
		{"example.com", "example.com", true},
		{"sub.example.com", "example.com", false},
		{"localhost", "localhost", true},
		{"192.168.1.1", "", false},
		{"2001:db8::1", "", false},
	}

	for _, test := range tests {
		gotRoot, gotApex := RootDomain(test.host)
		if gotRoot != test.wantRoot || gotApex != test.wantApex {
			t.Errorf("RootDomain(%q) = (%q, %v), want (%q, %v)", test.host, gotRoot, gotApex, test.wantRoot, test.wantApex)
		}
	}
}
