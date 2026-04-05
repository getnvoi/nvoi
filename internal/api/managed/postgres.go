package managed

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func init() { Register(&Postgres{}) }

// Postgres is a managed PostgreSQL service.
type Postgres struct{}

func (Postgres) Kind() string { return "postgres" }

func (Postgres) Spec(name string) config.Service {
	return config.Service{
		Image: "postgres:17",
		Port:  5432,
		Volumes: []string{
			name + "-data:/var/lib/postgresql/data",
		},
		Env: []string{
			"POSTGRES_USER=postgres",
			"POSTGRES_DB=" + name,
		},
		Secrets: []string{
			"POSTGRES_PASSWORD",
		},
	}
}

func (Postgres) Credentials(name string) map[string]string {
	password := RandomHex(16)
	return map[string]string{
		"HOST":     name,
		"PORT":     "5432",
		"USER":     "postgres",
		"PASSWORD": password,
		"DATABASE": name,
		"URL":      fmt.Sprintf("postgres://postgres:%s@%s:5432/%s", password, name, name),
	}
}

func (Postgres) EnvPrefix() string { return "DATABASE" }
