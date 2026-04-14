package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// resolveCredentials reads pre-resolved credentials from DeployContext.
// Credentials are resolved at the OS boundary (internal/core/resolve.go)
// and passed through DeployContext. This package never reads env vars.
func resolveCredentials(dc *config.DeployContext, name string, engine Engine) (*config.DatabaseCredentials, error) {
	creds := dc.DatabaseCreds[name]
	if creds == nil {
		return nil, fmt.Errorf("no credentials resolved for database %q", name)
	}

	prefix := strings.ToUpper(name)
	userEnv, passEnv, dbEnv := engine.EnvVarNames()

	if creds.User == "" {
		return nil, fmt.Errorf("%s_%s is required in environment", prefix, userEnv)
	}
	if creds.Password == "" {
		return nil, fmt.Errorf("%s_%s is required in environment", prefix, passEnv)
	}
	if creds.DBName == "" {
		return nil, fmt.Errorf("%s_%s is required in environment", prefix, dbEnv)
	}

	return creds, nil
}

func storeCredentials(ctx context.Context, ssh utils.SSHClient, ns, name string, engine Engine, creds *config.DatabaseCredentials, dbURL string) error {
	secretName := name + "-db-credentials"
	prefix := strings.ToUpper(name)
	userEnv, passEnv, dbEnv := engine.EnvVarNames()

	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	engName := strings.ToUpper(engine.Name())
	kvs := map[string]string{
		prefix + "_" + userEnv:           creds.User,
		prefix + "_" + passEnv:           creds.Password,
		prefix + "_" + dbEnv:             creds.DBName,
		prefix + "_DATABASE_URL":         dbURL,
		prefix + "_" + engName + "_HOST": name + "-db",
		prefix + "_" + engName + "_PORT": fmt.Sprintf("%d", engine.Port()),
	}
	for k, v := range kvs {
		if err := kube.UpsertSecretKey(ctx, ssh, ns, secretName, k, v); err != nil {
			return err
		}
	}
	return nil
}
