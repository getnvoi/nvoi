package managed

import (
	"fmt"

	"github.com/getnvoi/nvoi/internal/api/config"
)

func init() { Register(&Redis{}) }

// Redis is a managed Redis service.
type Redis struct{}

func (Redis) Kind() string { return "redis" }

func (Redis) Spec(name string) config.Service {
	return config.Service{
		Image: "redis:7-alpine",
		Port:  6379,
	}
}

func (Redis) Credentials(name string) map[string]string {
	return map[string]string{
		"HOST": name,
		"PORT": "6379",
		"URL":  fmt.Sprintf("redis://%s:6379", name),
	}
}

func (Redis) EnvPrefix() string { return "REDIS" }
