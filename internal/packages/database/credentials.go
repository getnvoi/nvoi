package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/viper"
)

// Credentials holds the database credentials.
type Credentials struct {
	User     string
	Password string
	DBName   string
}

// resolveCredentials reads database credentials from environment variables.
// All three are required — no auto-generation, no cluster fallback.
// The user owns the credentials.
func resolveCredentials(name string, engine Engine) (*Credentials, error) {
	prefix := strings.ToUpper(name)
	v := viper.GetViper()
	userEnv, passEnv, dbEnv := engine.EnvVarNames()

	creds := &Credentials{
		User:     v.GetString(prefix + "_" + userEnv),
		Password: v.GetString(prefix + "_" + passEnv),
		DBName:   v.GetString(prefix + "_" + dbEnv),
	}

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

func storeCredentials(ctx context.Context, ssh utils.SSHClient, ns, name string, engine Engine, creds *Credentials, dbURL string) error {
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
