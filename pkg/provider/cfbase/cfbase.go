// Package cfbase provides a shared Cloudflare API client constructor.
// Used by dns/cloudflare and storage/cloudflare.
package cfbase

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const BaseURL = "https://api.cloudflare.com/client/v4"

// NewAPI creates a Cloudflare HTTP client with Bearer token auth.
func NewAPI(apiKey, label string) *utils.HTTPClient {
	return &utils.HTTPClient{
		BaseURL: BaseURL,
		SetAuth: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+apiKey)
		},
		Label: label,
	}
}
