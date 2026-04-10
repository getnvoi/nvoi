package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/viper"
)

// Credentials holds the resolved database credentials.
type Credentials struct {
	User     string
	Password string
	DBName   string
}

// resolveCredentials resolves database credentials in priority order:
// 1. Environment variable (viper)
// 2. Existing k8s secret in cluster
// 3. Auto-generate
func resolveCredentials(ctx context.Context, dc *config.DeployContext, name string, engine Engine) (*Credentials, error) {
	prefix := strings.ToUpper(name)
	v := viper.GetViper()
	userEnv, passEnv, dbEnv := engine.EnvVarNames()

	creds := &Credentials{
		User:     v.GetString(prefix + "_" + userEnv),
		Password: v.GetString(prefix + "_" + passEnv),
		DBName:   v.GetString(prefix + "_" + dbEnv),
	}

	// Fill gaps from existing k8s secret
	if creds.User == "" || creds.Password == "" || creds.DBName == "" {
		existing := readExistingCredentials(ctx, dc, name, engine)
		if existing != nil {
			if creds.User == "" {
				creds.User = existing.User
			}
			if creds.Password == "" {
				creds.Password = existing.Password
			}
			if creds.DBName == "" {
				creds.DBName = existing.DBName
			}
		}
	}

	// Auto-generate remaining
	if creds.User == "" {
		creds.User = randomHex(6)
	}
	if creds.Password == "" {
		creds.Password = randomHex(32)
	}
	if creds.DBName == "" {
		creds.DBName = randomHex(6)
	}

	return creds, nil
}

func readExistingCredentials(ctx context.Context, dc *config.DeployContext, name string, engine Engine) *Credentials {
	ssh, names, err := dc.Cluster.SSH(ctx)
	if err != nil {
		return nil
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := name + "-db-credentials"
	prefix := strings.ToUpper(name)

	userEnv, passEnv, dbEnv := engine.EnvVarNames()
	user, err := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_"+userEnv)
	if err != nil || user == "" {
		return nil
	}
	pass, _ := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_"+passEnv)
	dbname, _ := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_"+dbEnv)

	return &Credentials{
		User:     strings.TrimSpace(user),
		Password: strings.TrimSpace(pass),
		DBName:   strings.TrimSpace(dbname),
	}
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

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
