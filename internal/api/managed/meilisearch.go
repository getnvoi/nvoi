package managed

import (
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func init() { Register(&Meilisearch{}) }

// Meilisearch is a managed Meilisearch service.
type Meilisearch struct{}

func (Meilisearch) Kind() string { return "meilisearch" }

func (Meilisearch) Spec(name string) config.Service {
	secretKey := "MEILI_MASTER_KEY_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return config.Service{
		Image: "getmeili/meilisearch:latest",
		Port:  7700,
		Volumes: []string{
			name + "-data:/meili_data",
		},
		Env: []string{
			"MEILI_ENV=production",
		},
		Secrets: []string{
			"MEILI_MASTER_KEY=" + secretKey,
		},
	}
}

func (Meilisearch) Credentials(name string) map[string]string {
	masterKey := RandomHex(16)
	return map[string]string{
		"HOST":       name,
		"PORT":       "7700",
		"MASTER_KEY": masterKey,
		"URL":        fmt.Sprintf("http://%s:7700", name),
	}
}

func (Meilisearch) EnvPrefix() string { return "MEILI" }

func (Meilisearch) InternalSecrets(name string, creds map[string]string) map[string]string {
	key := "MEILI_MASTER_KEY_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	return map[string]string{
		key: creds["MASTER_KEY"],
	}
}
