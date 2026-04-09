package cli

import "net/url"

// providerKinds is the single source of truth for the four provider kinds.
// Used by repos use flags, provider commands, and pushConfig.
var providerKinds = []string{"compute", "dns", "storage", "build"}

// CloudBackend implements commands.Backend by calling the nvoi API.
// Operational commands call API endpoints directly.
// Config-mutation commands (set/delete) load config, mutate, and push.
type CloudBackend struct {
	client *APIClient
	wsID   string
	repoID string
}

// buildCloudBackend loads auth and returns an authenticated CloudBackend.
func buildCloudBackend() (*CloudBackend, error) {
	client, cfg, err := authedClient()
	if err != nil {
		return nil, err
	}
	wsID, repoID, err := requireRepo(cfg)
	if err != nil {
		return nil, err
	}
	return &CloudBackend{client: client, wsID: wsID, repoID: repoID}, nil
}

// repoPath builds the scoped API path for a resource under the active repo.
func (c *CloudBackend) repoPath(suffix string) string {
	return "/workspaces/" + c.wsID + "/repos/" + c.repoID + suffix
}

// esc escapes a user-controlled value for safe use in URL paths.
func esc(s string) string {
	return url.PathEscape(s)
}
