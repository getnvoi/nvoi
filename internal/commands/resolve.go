package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/spf13/viper"
)

func ResolveAppEnv() (string, string, error) {
	app := viper.GetString("app")
	env := viper.GetString("env")
	if app == "" || env == "" {
		return "", "", fmt.Errorf("app and env are required in config YAML")
	}
	return app, env, nil
}

func ResolveProvider(kind string) string {
	return viper.GetString("providers." + kind)
}

func ResolveProviderCredentials(kind, name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	schema, err := provider.GetSchema(kind, name)
	if err != nil {
		return nil, err
	}
	creds := make(map[string]string, len(schema.Fields))
	for _, f := range schema.Fields {
		if v := os.Getenv(f.EnvVar); v != "" {
			creds[f.Key] = v
		}
	}
	return creds, nil
}

func ResolveSSHKey() ([]byte, error) {
	keyPath := os.Getenv("SSH_KEY_PATH")
	if keyPath != "" {
		keyPath = expandHome(keyPath)
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
		}
		return key, nil
	}
	home := os.Getenv("HOME")
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := home + "/.ssh/" + name
		if key, err := os.ReadFile(p); err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("no SSH key found — set SSH_KEY_PATH or place a key at ~/.ssh/id_ed25519 or ~/.ssh/id_rsa")
}

func ResolveGitAuth() (string, string) {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		token := strings.TrimSpace(string(out))
		if token != "" {
			return "x-access-token", token
		}
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return "x-access-token", token
	}
	return "", ""
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home := os.Getenv("HOME"); home != "" {
			return home + path[1:]
		}
	}
	return path
}
