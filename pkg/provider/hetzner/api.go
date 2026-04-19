// Package hetznerbase provides a shared Hetzner Cloud API client constructor.
// Used by compute/hetzner (and future domain packages).
package hetzner

import (
	"net/http"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const BaseURL = "https://api.hetzner.cloud/v1"

// NewAPI creates a Hetzner Cloud HTTP client with Bearer token auth.
func NewAPI(token string) *utils.HTTPClient {
	return &utils.HTTPClient{
		BaseURL: BaseURL,
		SetAuth: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
		Label:   "hetzner",
	}
}
