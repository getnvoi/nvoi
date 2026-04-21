// Package scwbase provides a shared Scaleway API client constructor.
// Used by compute/scaleway and dns/scaleway.
package scaleway

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const BaseURL = "https://api.scaleway.com"

// NewAPI creates a Scaleway HTTP client with X-Auth-Token auth.
func NewAPI(secretKey, label string) *utils.HTTPClient {
	return &utils.HTTPClient{
		BaseURL: BaseURL,
		SetAuth: func(r *http.Request) {
			r.Header.Set("X-Auth-Token", secretKey)
		},
		Label: label,
	}
}
