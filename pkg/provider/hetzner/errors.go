package hetzner

import (
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// classify maps Hetzner API errors onto the shared infra sentinels.
// Wired into *utils.HTTPClient.Classify at client construction so every
// call to c.api.Do returns errors that already satisfy errors.Is —
// callers never touch err.Error() for classification again.
//
// Status code beats body text where Hetzner provides one. Body-string
// matching survives for cases Hetzner surfaces only via body ("already
// added", "not attached", "not found" on resource-scoped action calls)
// — but it lives exactly once, here, not scattered across volume.go /
// firewall.go / server.go with five different spellings.
func classify(e *utils.APIError) error {
	if e.Status == 423 {
		return infra.ErrLocked
	}
	if e.Status == 404 {
		return utils.ErrNotFound
	}
	switch {
	case strings.Contains(e.Body, "already added"):
		return infra.ErrAlreadyAttached
	case strings.Contains(e.Body, "not attached"):
		return infra.ErrNotAttached
	case strings.Contains(e.Body, "not found"):
		return utils.ErrNotFound
	}
	return nil
}
