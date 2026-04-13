package core

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	s3client "github.com/getnvoi/nvoi/pkg/utils/s3"
)

// ── Backup List ─────────────────────────────────────────────────────────────

type DatabaseBackupListRequest struct {
	Cluster
	DBName string
}

type BackupEntry struct {
	Key          string `json:"key"`
	Size         int64  `json:"size"`
	LastModified string `json:"last_modified"`
}

func DatabaseBackupList(ctx context.Context, req DatabaseBackupListRequest) ([]BackupEntry, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, err
	}
	defer ssh.Close()

	endpoint, bucket, accessKey, secretKey, err := backupCreds(ctx, ssh, names, req.DBName)
	if err != nil {
		return nil, err
	}

	objects, err := s3client.ListObjects(endpoint, accessKey, secretKey, bucket, "backups/")
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}

	entries := make([]BackupEntry, len(objects))
	for i, obj := range objects {
		entries[i] = BackupEntry{
			Key:          obj.Key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		}
	}
	return entries, nil
}

// ── Backup Download ─────────────────────────────────────────────────────────

type DatabaseBackupDownloadRequest struct {
	Cluster
	DBName string
	Key    string
}

func DatabaseBackupDownload(ctx context.Context, req DatabaseBackupDownloadRequest) (io.ReadCloser, int64, error) {
	// SSH is only needed to read backup credentials from k8s secrets.
	// Close it before returning — the S3 stream is a direct HTTP connection,
	// not tunneled through SSH.
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return nil, 0, err
	}
	endpoint, bucket, accessKey, secretKey, err := backupCreds(ctx, ssh, names, req.DBName)
	ssh.Close()
	if err != nil {
		return nil, 0, err
	}

	key := req.Key
	if !strings.HasPrefix(key, "backups/") {
		key = "backups/" + key
	}

	body, _, contentLength, err := s3client.GetStream(endpoint, accessKey, secretKey, bucket, key)
	if err != nil {
		return nil, 0, fmt.Errorf("download %s: %w", key, err)
	}
	return body, contentLength, nil
}

// ── SQL ─────────────────────────────────────────────────────────────────────

type DatabaseSQLRequest struct {
	Cluster
	DBName string
	Engine string // "postgres" or "mysql" — from config kind
	Query  string
}

func DatabaseSQL(ctx context.Context, req DatabaseSQLRequest) (string, error) {
	ssh, names, err := req.Cluster.SSH(ctx)
	if err != nil {
		return "", err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	pod := req.DBName + "-db-0"
	secretName := req.DBName + "-db-credentials"
	prefix := utils.ToUpperSnake(req.DBName)

	var user, dbname, sqlCmd string
	switch req.Engine {
	case "postgres":
		user, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_POSTGRES_USER")
		dbname, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_POSTGRES_DB")
		if user == "" {
			return "", ErrNotReady(fmt.Sprintf("postgres credentials not found for %q — has the database been deployed?", req.DBName))
		}
		sqlCmd = fmt.Sprintf("exec %s -- psql -U %s -d %s -c %q", pod, user, dbname, req.Query)
	case "mysql":
		user, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_MYSQL_USER")
		dbname, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, prefix+"_MYSQL_DATABASE")
		if user == "" {
			return "", ErrNotReady(fmt.Sprintf("mysql credentials not found for %q — has the database been deployed?", req.DBName))
		}
		sqlCmd = fmt.Sprintf("exec %s -- mysql -u %s %s -e %q", pod, user, dbname, req.Query)
	default:
		return "", ErrInputf("unsupported database engine %q for %q", req.Engine, req.DBName)
	}

	out, err := kube.RunKubectl(ctx, ssh, ns, sqlCmd)
	if err != nil {
		return "", fmt.Errorf("sql: %w", err)
	}
	return string(out), nil
}

// ── Internal helpers ────────────────────────────────────────────────────────

func backupCreds(ctx context.Context, ssh utils.SSHClient, names *utils.Names, dbName string) (endpoint, bucket, accessKey, secretKey string, err error) {
	ns := names.KubeNamespace()
	bucketName := dbName + "-db-backups"
	secretName := names.KubeSecrets()

	storageKey := func(suffix string) string {
		upper := utils.ToUpperSnake(bucketName)
		return "STORAGE_" + upper + "_" + suffix
	}

	endpoint, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, storageKey("ENDPOINT"))
	bucket, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, storageKey("BUCKET"))
	accessKey, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, storageKey("ACCESS_KEY_ID"))
	secretKey, _ = kube.GetSecretValue(ctx, ssh, ns, secretName, storageKey("SECRET_ACCESS_KEY"))

	if endpoint == "" || bucket == "" {
		return "", "", "", "", ErrNotReady("backup bucket credentials not found — has the database been deployed?")
	}
	return endpoint, bucket, accessKey, secretKey, nil
}
