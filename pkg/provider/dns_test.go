package provider

import "testing"

func TestRecordName(t *testing.T) {
	tests := []struct {
		domain string
		zone   string
		want   string
	}{
		{"myapp.com", "myapp.com", "@"},
		{"api.myapp.com", "myapp.com", "api"},
		{"other.com", "myapp.com", "other.com"},
	}
	for _, tt := range tests {
		got := RecordName(tt.domain, tt.zone)
		if got != tt.want {
			t.Errorf("RecordName(%q, %q) = %q, want %q", tt.domain, tt.zone, got, tt.want)
		}
	}
}

func TestRecordType(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"1.2.3.4", "A"},
		{"::1", "AAAA"},
		{"2001:db8::1", "AAAA"},
	}
	for _, tt := range tests {
		got := RecordType(tt.ip)
		if got != tt.want {
			t.Errorf("RecordType(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}
}
