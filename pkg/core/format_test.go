package core

import (
	"fmt"
	"testing"
	"time"
)

func TestObfuscate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"a", "•"},
		{"ab", "••"},
		{"abcd", "••••"},
		{"abcde", "•bcde"},
		{"sk-1234567890", "•••••••••7890"},
		{"hello", "•ello"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.input), func(t *testing.T) {
			got := Obfuscate(tt.input)
			if got != tt.want {
				t.Errorf("Obfuscate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHumanAge(t *testing.T) {
	tests := []struct {
		name   string
		offset time.Duration
		want   string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 3 * time.Hour, "3h"},
		{"days", 72 * time.Hour, "3d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := time.Now().Add(-tt.offset).Format(time.RFC3339)
			got := HumanAge(ts)
			if got != tt.want {
				t.Errorf("HumanAge(%q) = %q, want %q", ts, got, tt.want)
			}
		})
	}
}

func TestHumanAgeInvalid(t *testing.T) {
	got := HumanAge("not-a-timestamp")
	if got != "" {
		t.Errorf("HumanAge(invalid) = %q, want empty string", got)
	}
}
