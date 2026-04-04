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
	APIBase  string `json:"api_base"`
	Token    string `json:"token"`
	Username string `json:"username"`
}

func authConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, authConfigDir, authConfigFile)
}

func LoadAuthConfig() (*AuthConfig, error) {
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
	return &cfg, nil
}

func SaveAuthConfig(cfg *AuthConfig) error {
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
