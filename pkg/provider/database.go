package provider

import (
	"context"
	"io"

	"github.com/getnvoi/nvoi/pkg/kube"
	"k8s.io/apimachinery/pkg/runtime"
)

// DatabaseProvider owns narrow database lifecycle only: ensure creds,
// converge provider state, exec SQL, and handle backups.
type DatabaseProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureCredentials(ctx context.Context, kc *kube.Client, req DatabaseRequest) (DatabaseCredentials, error)
	Reconcile(ctx context.Context, req DatabaseRequest) (*DatabasePlan, error)
	Delete(ctx context.Context, req DatabaseRequest) error
	ExecSQL(ctx context.Context, req DatabaseRequest, stmt string) (*SQLResult, error)
	BackupNow(ctx context.Context, req DatabaseRequest) (*BackupRef, error)
	ListBackups(ctx context.Context, req DatabaseRequest) ([]BackupRef, error)
	DownloadBackup(ctx context.Context, req DatabaseRequest, backupID string, w io.Writer) error
	ListResources(ctx context.Context) ([]ResourceGroup, error)
	Close() error
}

type DatabaseRequest struct {
	Name                  string
	FullName              string
	Namespace             string
	PodName               string
	PVCName               string
	BackupName            string
	CredentialsSecretName string
	Labels                map[string]string
	Spec                  DatabaseSpec
	Bucket                *BucketHandle
	DeleteVolumes         bool
	Kube                  *kube.Client
	Log                   EventSink
}

type DatabaseSpec struct {
	Engine   string
	Version  string
	Server   string
	Size     int
	User     string
	Password string
	Database string
	Region   string
	Backup   *DatabaseBackupSpec
}

type DatabaseBackupSpec struct {
	Schedule  string
	Retention int
	Storage   string
}

type BucketHandle struct {
	Name string
}

type DatabasePlan struct {
	Workloads []runtime.Object
}

type DatabaseCredentials struct {
	URL      string
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

type SQLResult struct {
	Columns      []string
	Rows         [][]string
	RowsAffected int64
}

type BackupRef struct {
	ID        string
	CreatedAt string
	SizeBytes int64
	Kind      string
}

var databaseRegistry = newRegistry[DatabaseProvider]("database")

func RegisterDatabase(name string, schema CredentialSchema, factory func(creds map[string]string) DatabaseProvider) {
	databaseRegistry.register(name, schema, factory)
}

func GetDatabaseSchema(name string) (CredentialSchema, error) {
	return databaseRegistry.getSchema(name)
}

func ResolveDatabase(name string, creds map[string]string) (DatabaseProvider, error) {
	return databaseRegistry.resolve(name, creds)
}
