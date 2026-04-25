package scaleway

import (
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider/infra"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// classify maps Scaleway API errors onto the shared infra sentinels.
// Wired into *utils.HTTPClient.Classify at client construction — every
// c.api.Do / c.doInstance call then returns errors that satisfy
// errors.Is against infra.ErrInUse / utils.ErrNotFound / … so callers
// branch without inspecting err.Error() text.
//
// Scaleway surfaces "group is in use" as HTTP 400 with a message body
// rather than a dedicated status code. Body-string matching survives
// for that case, but it lives exactly once — here — instead of
// scattered across firewall.go / server.go.
func classify(e *utils.APIError) error {
	if e.Status == 404 {
		return utils.ErrNotFound
	}
	if strings.Contains(e.Body, "in use") {
		return infra.ErrInUse
	}
	return nil
}
