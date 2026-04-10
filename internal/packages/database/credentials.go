package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
func resolveCredentials(ctx context.Context, dc *config.DeployContext, name string) (*Credentials, error) {
	prefix := strings.ToUpper(name)
	v := viper.GetViper()

	creds := &Credentials{
		User:     v.GetString(prefix + "_POSTGRES_USER"),
		Password: v.GetString(prefix + "_POSTGRES_PASSWORD"),
		DBName:   v.GetString(prefix + "_POSTGRES_DB"),
	}

	// Fill gaps from existing k8s secret
	if creds.User == "" || creds.Password == "" || creds.DBName == "" {
		existing := readExistingCredentials(ctx, dc, name)
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

func readExistingCredentials(ctx context.Context, dc *config.DeployContext, name string) *Credentials {
	ssh, names, err := dc.Cluster.SSH(ctx)
	if err != nil {
		return nil
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	secretName := name + "-db-credentials"
	prefix := strings.ToUpper(name)

	user, err := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_POSTGRES_USER")
	if err != nil || user == "" {
		return nil
	}
	pass, _ := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_POSTGRES_PASSWORD")
	dbname, _ := kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_POSTGRES_DB")

	return &Credentials{
		User:     strings.TrimSpace(user),
		Password: strings.TrimSpace(pass),
		DBName:   strings.TrimSpace(dbname),
	}
}

func storeCredentials(ctx context.Context, ssh utils.SSHClient, ns, name string, creds *Credentials, dbURL string) error {
	secretName := name + "-db-credentials"
	prefix := strings.ToUpper(name)

	if err := kube.EnsureNamespace(ctx, ssh, ns); err != nil {
		return err
	}

	kvs := map[string]string{
		prefix + "_POSTGRES_USER":     creds.User,
		prefix + "_POSTGRES_PASSWORD": creds.Password,
		prefix + "_POSTGRES_DB":       creds.DBName,
		prefix + "_DATABASE_URL":      dbURL,
		prefix + "_POSTGRES_HOST":     name + "-db",
		prefix + "_POSTGRES_PORT":     "5432",
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
