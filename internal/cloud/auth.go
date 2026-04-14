package cloud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

var (
	authOnce   sync.Once
	authCached *AuthConfig
	authErr    error
)

// ResetAuthCache clears the cached auth config so the next LoadAuthConfig
// re-reads from disk. Safe to call from any goroutine.
func ResetAuthCache() {
	authOnce = sync.Once{}
	authCached = nil
	authErr = nil
}

func LoadAuthConfig() (*AuthConfig, error) {
	authOnce.Do(func() {
		data, err := os.ReadFile(authConfigPath())
		if err != nil {
			authErr = fmt.Errorf("not logged in — run 'nvoi login'")
			return
		}
		var cfg AuthConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			authErr = fmt.Errorf("corrupt auth config: %w", err)
			return
		}
		if cfg.Token == "" {
			authErr = fmt.Errorf("not logged in — run 'nvoi login'")
			return
		}
		authCached = &cfg
	})
	return authCached, authErr
}

func SaveAuthConfig(cfg *AuthConfig) error {
	ResetAuthCache() // invalidate — next LoadAuthConfig re-reads from disk
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
