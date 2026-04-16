package core

import (
	"bytes"
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
	names, err := req.Cluster.Names()
	if err != nil {
		return nil, err
	}

	endpoint, bucket, accessKey, secretKey, err := backupCreds(ctx, req.Kube, names, req.DBName)
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
	names, err := req.Cluster.Names()
	if err != nil {
		return nil, 0, err
	}

	endpoint, bucket, accessKey, secretKey, err := backupCreds(ctx, req.Kube, names, req.DBName)
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
	names, err := req.Cluster.Names()
	if err != nil {
		return "", err
	}

	ns := names.KubeNamespace()
	pod := utils.DatabasePodName(req.DBName)
	secretName := utils.DatabaseSecretName(req.DBName)
	prefix := utils.ToUpperSnake(req.DBName)

	var user, dbname string
	var command []string
	switch req.Engine {
	case "postgres":
		user, _ = req.Kube.GetSecretValue(ctx, ns, secretName, prefix+"_POSTGRES_USER")
		dbname, _ = req.Kube.GetSecretValue(ctx, ns, secretName, prefix+"_POSTGRES_DB")
		if user == "" {
			return "", ErrNotReady(fmt.Sprintf("postgres credentials not found for %q — has the database been deployed?", req.DBName))
		}
		command = []string{"psql", "-U", user, "-d", dbname, "-c", req.Query}
	case "mysql":
		user, _ = req.Kube.GetSecretValue(ctx, ns, secretName, prefix+"_MYSQL_USER")
		dbname, _ = req.Kube.GetSecretValue(ctx, ns, secretName, prefix+"_MYSQL_DATABASE")
		if user == "" {
			return "", ErrNotReady(fmt.Sprintf("mysql credentials not found for %q — has the database been deployed?", req.DBName))
		}
		command = []string{"mysql", "-u", user, dbname, "-e", req.Query}
	default:
		return "", ErrInputf("unsupported database engine %q for %q", req.Engine, req.DBName)
	}

	var buf bytes.Buffer
	if err := req.Kube.ExecInPod(ctx, ns, pod, command, &buf, &buf); err != nil {
		return "", fmt.Errorf("sql: %w", err)
	}
	return buf.String(), nil
}

// ── Internal helpers ────────────────────────────────────────────────────────

func backupCreds(ctx context.Context, kc *kube.KubeClient, names *utils.Names, dbName string) (endpoint, bucket, accessKey, secretKey string, err error) {
	ns := names.KubeNamespace()
	bucketName := utils.DatabaseBackupBucket(dbName)
	secretName := utils.DatabaseBackupCredsSecret(dbName)

	storageKey := func(suffix string) string {
		upper := utils.ToUpperSnake(bucketName)
		return "STORAGE_" + upper + "_" + suffix
	}

	endpoint, _ = kc.GetSecretValue(ctx, ns, secretName, storageKey("ENDPOINT"))
	bucket, _ = kc.GetSecretValue(ctx, ns, secretName, storageKey("BUCKET"))
	accessKey, _ = kc.GetSecretValue(ctx, ns, secretName, storageKey("ACCESS_KEY_ID"))
	secretKey, _ = kc.GetSecretValue(ctx, ns, secretName, storageKey("SECRET_ACCESS_KEY"))

	if endpoint == "" || bucket == "" {
		return "", "", "", "", ErrNotReady("backup bucket credentials not found — has the database been deployed?")
	}
	return endpoint, bucket, accessKey, secretKey, nil
}
