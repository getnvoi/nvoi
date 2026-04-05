package managed

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func init() { Register(&Meilisearch{}) }

// Meilisearch is a managed Meilisearch service.
type Meilisearch struct{}

func (Meilisearch) Kind() string { return "meilisearch" }

func (Meilisearch) Spec(name string) config.Service {
	return config.Service{
		Image: "getmeili/meilisearch:v1",
		Port:  7700,
		Volumes: []string{
			name + "-data:/meili_data",
		},
		Env: []string{
			"MEILI_ENV=production",
		},
		Secrets: []string{
			"MEILI_MASTER_KEY",
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
