// Package scwbase provides a shared Scaleway API client constructor.
// Used by compute/scaleway and dns/scaleway.
package scaleway

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const BaseURL = "https://api.scaleway.com"

// NewAPI creates a Scaleway HTTP client with X-Auth-Token auth and the
// Scaleway-specific error classifier wired in. Every c.api.Do /
// c.doInstance call therefore returns errors pre-wrapped with shared
// infra sentinels (see errors.go) so callers branch via errors.Is.
func NewAPI(secretKey, label string) *utils.HTTPClient {
	return &utils.HTTPClient{
		BaseURL: BaseURL,
		SetAuth: func(r *http.Request) {
			r.Header.Set("X-Auth-Token", secretKey)
		},
		Label:    label,
		Classify: classify,
	}
}
