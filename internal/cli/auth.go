package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const authConfigDir = ".config/nvoi"
const authConfigFile = "auth.json"

type AuthConfig struct {
	APIBase     string `json:"api_base"`
	Token       string `json:"token"`
	Username    string `json:"username"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	RepoID      string `json:"repo_id,omitempty"`
}

func authConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, authConfigDir, authConfigFile)
}

// cachedAuth holds the result of the first successful LoadAuthConfig call.
// Avoids re-reading ~/.config/nvoi/auth.json on every command — the gate
// check in addCloudOnly loads it once, AuthedClient() in RunE reuses it.
var cachedAuth *AuthConfig

// ResetAuthCache clears the cached auth config. Used by tests that
// write different auth files between test cases.
func ResetAuthCache() { cachedAuth = nil }

func LoadAuthConfig() (*AuthConfig, error) {
	if cachedAuth != nil {
		return cachedAuth, nil
	}
	data, err := os.ReadFile(authConfigPath())
	if err != nil {
		return nil, fmt.Errorf("not logged in — run 'nvoi login'")
	}
	var cfg AuthConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("corrupt auth config: %w", err)
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("not logged in — run 'nvoi login'")
	}
	cachedAuth = &cfg
	return &cfg, nil
}

func SaveAuthConfig(cfg *AuthConfig) error {
	cachedAuth = nil // invalidate cache — next LoadAuthConfig re-reads from disk
	path := authConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
