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
	// Restore replays a backup (identified by its bucket key) into the
	// database. Every implementation launches the uniform restore Job
	// (same image as backup, MODE=restore), so selfhosted and SaaS
	// engines share one transport: the Job connects to $DATABASE_URL
	// over the wire — in-cluster DNS for selfhosted, external TLS for
	// neon/planetscale — and pipes the object through `gunzip | psql`
	// or `gunzip | mysql`. Zero engine-specific restore code outside
	// the backup image.
	//
	// Used by `nvoi database restore` directly and `nvoi database
	// migrate` as its final step after teardown + apply.
	Restore(ctx context.Context, req DatabaseRequest, backupKey string) error
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
	// BackupCredsSecretName is the cluster-side Secret that holds the
	// bucket credentials the backup CronJob / one-shot Job envFroms. Set
	// by the reconciler when Spec.Backup != nil and providers.storage is
	// configured. Shape of the Secret's Data keys:
	//   BUCKET_ENDPOINT, BUCKET_NAME, AWS_ACCESS_KEY_ID,
	//   AWS_SECRET_ACCESS_KEY, AWS_REGION
	// Plus ENGINE (per-database) and DATABASE_URL (mirrored from the
	// credentials Secret so the backup tool has a single source).
	BackupCredsSecretName string
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

// DatabaseBackupSpec is the reconciled backup policy for one database.
// The bucket itself is provisioned implicitly by the reconciler — the
// spec carries only what the provider's Reconcile needs to decide
// whether to emit a CronJob and what schedule / retention to set.
type DatabaseBackupSpec struct {
	Schedule  string
	Retention int
}

type BucketHandle struct {
	Name        string
	Credentials BucketCredentials
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
