package core

import (
	"fmt"
	"strings"
	"time"
)

// Obfuscate preserves the original length, replacing all but the last 4 chars with •.
func Obfuscate(s string) string {
	n := len(s)
	if n <= 4 {
		return strings.Repeat("•", n)
	}
	return strings.Repeat("•", n-4) + s[n-4:]
}

// HumanAge converts an RFC3339 timestamp to a human-friendly age string (e.g. "5m", "2h", "3d").
func HumanAge(timestamp string) string {
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
