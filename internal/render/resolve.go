package render

import (
	"os"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// Resolve picks the right renderer based on flags and terminal detection.
func Resolve(jsonFlag, ciFlag bool) pkgcore.Output {
	if jsonFlag {
		return NewJSONOutput(os.Stdout)
	}
	if ciFlag || !IsTerminal() {
		return NewPlainOutput()
	}
	return NewTUIOutput()
}

// IsTerminal returns true if stdout is a TTY.
func IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
