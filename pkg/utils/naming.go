// Package utils is the single source of truth for all resource names.
//
// Every resource nvoi creates is named deterministically from NVOI_ENV.
// Same env → same names → same resources. No UUIDs, no state files.
// Idempotency through naming: always call GetXByName(names.X(...)).
// If found → reuse. If not → create.
package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// ── Names ────────────────────────────────────────────────────────────────────────

// Names provides all resource names for an app + environment.
// NVOI_APP_NAME=dummy-rails NVOI_ENV=production → nvoi-dummy-rails-production-*.
// Different app or env = brand new infrastructure.
type Names struct {
	app string
	env string
}

// NewNames creates Names from app + env strings.
// Caller (cmd layer) is responsible for reading env vars.
func NewNames(app, env string) (*Names, error) {
	if app == "" || env == "" {
		return nil, fmt.Errorf("app and env are required")
	}
	if err := ValidateName("app", app); err != nil {
		return nil, err
	}
	if err := ValidateName("env", env); err != nil {
		return nil, err
	}
	return &Names{app: app, env: env}, nil
}

func (n *Names) App() string { return n.app }
func (n *Names) Env() string { return n.env }

// ── Infrastructure names ───────────────────────────────────────────────────────

func (n *Names) Base() string            { return fmt.Sprintf("nvoi-%s-%s", n.app, n.env) }
func (n *Names) Firewall() string        { return n.Base() + "-fw" } // legacy — do not use for new code
func (n *Names) MasterFirewall() string  { return n.Base() + "-master-fw" }
func (n *Names) WorkerFirewall() string  { return n.Base() + "-worker-fw" }
func (n *Names) BuilderFirewall() string { return n.Base() + "-builder-fw" }

// FirewallForRole picks the per-role firewall name. Unknown roles fall back
// to the worker firewall (base rules: SSH + internal only) — conservative
// default so typos never open public ports.
func (n *Names) FirewallForRole(role string) string {
	switch role {
	case RoleMaster:
		return n.MasterFirewall()
	case RoleBuilder:
		return n.BuilderFirewall()
	default:
		return n.WorkerFirewall()
	}
}

// BuilderCacheVolume is the per-builder Docker data-root volume. Same naming
// scheme as user volumes (`<base>-<name>`) with a "-builder-cache" suffix so
// the orphan-sweep pattern keeps working without special casing. One cache
// volume per builder server keeps buildkit layer reuse scoped to the builder
// that created them — no cross-builder cache contention.
func (n *Names) BuilderCacheVolume(serverKey string) string {
	return fmt.Sprintf("%s-%s-builder-cache", n.Base(), serverKey)
}

// BuilderCacheVolumeShort returns the cache volume's short form — the name
// after the `<base>-` prefix is stripped. Providers' orphan-sweep and
// teardown helpers key on short names (same as user-declared volume keys
// in cfg.VolumeDefs). Keeping this derivation here avoids rebuilding the
// "-builder-cache" literal outside naming.go.
func (n *Names) BuilderCacheVolumeShort(serverKey string) string {
	return fmt.Sprintf("%s-builder-cache", serverKey)
}
func (n *Names) Network() string           { return n.Base() + "-net" }
func (n *Names) Server(key string) string  { return fmt.Sprintf("%s-%s", n.Base(), key) }
func (n *Names) Stack() string             { return n.Base() }
func (n *Names) Volume(name string) string { return fmt.Sprintf("%s-%s", n.Base(), name) }
func (n *Names) Bucket(name string) string { return fmt.Sprintf("%s-%s", n.Base(), name) }

// ── K8s ────────────────────────────────────────────────────────────────────────

// KubeNamespace — each app+env gets its own. Service names stay short.
// POSTGRES_HOST=db just works. No rewriting magic.
func (n *Names) KubeNamespace() string                { return n.Base() }
func (n *Names) KubeWorkload(svc string) string       { return svc }
func (n *Names) KubeService(svc string) string        { return svc }
func (n *Names) KubeSecrets() string                  { return "secrets" }
func (n *Names) KubeServiceSecrets(svc string) string { return svc + "-secrets" }
func (n *Names) Database(name string) string          { return fmt.Sprintf("%s-db-%s", n.Base(), name) }
func (n *Names) KubeDatabaseCredentials(name string) string {
	return n.Database(name) + "-credentials"
}
func (n *Names) KubeDatabasePVC(name string) string        { return n.Database(name) + "-data" }
func (n *Names) KubeDatabaseBackupCron(name string) string { return n.Database(name) + "-backup" }
func (n *Names) KubeDatabasePod(name string) string        { return n.Database(name) + "-0" }

// KubeDatabaseBackupBucket — one bucket per database, provisioned
// implicitly on `providers.storage` when `databases.<name>.backup` is set.
// Prefix-free: one bucket holds exactly one database's dumps, so a single
// `SetLifecycle(bucket, retention)` governs the whole retention policy.
func (n *Names) KubeDatabaseBackupBucket(name string) string {
	return n.Database(name) + "-backups"
}

// KubeDatabaseBackupCreds — cluster-side Secret envFrom'd by the backup
// CronJob / one-shot Job. Materializes the bucket's S3-compatible
// credentials (endpoint, key id, secret, region) so the dump tool
// uploads without a provider SDK.
func (n *Names) KubeDatabaseBackupCreds(name string) string {
	return n.Database(name) + "-backup-creds"
}

// KubeDatabaseSnapshot returns the VolumeSnapshot object name for a
// user-requested snapshot of a DB. `label` is free-form metadata
// (e.g. `pre-migration`); callers must pre-validate it with
// ValidateName — it lands in a kube object name.
//
// Distinct from KubeDatabaseBranchSnapshot below: user snapshots and
// branch snapshots have different purposes (audit / recovery vs clone
// lineage) and different lifecycles. Keeping the helpers separate
// stops the `br-` prefix from bleeding into caller code.
func (n *Names) KubeDatabaseSnapshot(dbName, label string) string {
	return n.KubeDatabasePVC(dbName) + "-snap-" + label
}

// KubeDatabaseBranch returns the base name shared by a branch's
// sibling StatefulSet, Service, and PVC. Every branch workload is
// named identically so consumer DNS (`{branch-name}:5432`) is
// obvious. Callers must pre-validate `branch` with ValidateName.
func (n *Names) KubeDatabaseBranch(dbName, branch string) string {
	return n.KubeDatabasePVC(dbName) + "-br-" + branch
}

// KubeDatabaseBranchSnapshot returns the VolumeSnapshot object name
// for the snapshot created as part of a Branch operation. Shares the
// `-br-` segment with KubeDatabaseBranch so `list` operations can
// trace a branch workload back to its seed snapshot by prefix alone.
// Callers must pre-validate `branch` with ValidateName.
func (n *Names) KubeDatabaseBranchSnapshot(dbName, branch string) string {
	return n.KubeDatabaseBranch(dbName, branch) + "-snap"
}

// CronJobRunName is the one-shot k8s Job name spawned by `nvoi cron run`.
// Suffix is unix-nanoseconds (caller passes time.Now().Unix() — here for
// the signature, CallerProvided so tests can inject deterministic values).
// Lives in naming.go so every resource nvoi creates gets its name here.
func (n *Names) CronJobRunName(cronName string, suffix int64) string {
	return fmt.Sprintf("%s-run-%d", cronName, suffix)
}

// ── Labels ─────────────────────────────────────────────────────────────────────

// Labels returns the canonical label set for PROVIDER-side resources —
// Hetzner servers, Scaleway volumes, AWS instances, etc. Bare keys
// (no `app.kubernetes.io/` prefix) because:
//   - These labels are matched against existing infra at lookup time
//     (c.ListServers(ctx, names.Labels())). Changing the key shape
//     would orphan every existing deployment from `infra.Connect`.
//   - Cloud-provider label key constraints differ; bare `managed-by`
//     works on every backend without per-cloud quirks.
//
// For KUBE-side resources, use KubeLabels — the proper k8s convention
// is required there so kube.NvoiSelector matches.
func (n *Names) Labels() map[string]string {
	return map[string]string{
		"managed-by": "nvoi",
		"app":        n.Base(),
		"env":        n.env,
	}
}

// KubeLabels returns the canonical label set for KUBE-side resources —
// Deployments, StatefulSets, Services, Secrets, CronJobs, PVCs. The
// managed-by key uses the `app.kubernetes.io/` prefix so kube.NvoiSelector
// matches; `app` and `env` mirror Labels() for cross-domain consistency.
//
// This is the label set the databases pipeline stamps on its workloads
// (req.Labels = names.KubeLabels()) so they participate in the same
// selector-driven queries as user services / crons.
func (n *Names) KubeLabels() map[string]string {
	return map[string]string{
		LabelAppManagedBy: LabelManagedBy,
		"app":             n.Base(),
		"env":             n.env,
	}
}

// ── Remote paths ───────────────────────────────────────────────────────────────

func (n *Names) VolumeMountPath(name string) string {
	return fmt.Sprintf("/mnt/data/%s-%s", n.Base(), name)
}

func (n *Names) NamedVolumeHostPath(volume string) string {
	return fmt.Sprintf("/var/lib/nvoi/volumes/%s/%s", n.Stack(), volume)
}

// ── K3s paths ──────────────────────────────────────────────────────────────────

const (
	KubeconfigPath = "/etc/rancher/k3s/k3s.yaml"
	K3sTokenPath   = "/var/lib/rancher/k3s/server/node-token"
)

// ── Remote file paths ──────────────────────────────────────────────────────────

func KubeManifestPath() string { return fmt.Sprintf("/home/%s/nvoi-k8s.yaml", DefaultUser) }
func EnvFilePath() string      { return fmt.Sprintf("/home/%s/.nvoi.env", DefaultUser) }
func DeployKeyPath() string    { return fmt.Sprintf("/home/%s/.ssh/nvoi_deploy_key", DefaultUser) }

// ── K8s label keys ─────────────────────────────────────────────────────────────

const (
	LabelAppName        = "app.kubernetes.io/name"
	LabelAppManagedBy   = "app.kubernetes.io/managed-by"
	LabelManagedBy      = "nvoi"
	LabelNvoiService    = "nvoi/service"
	LabelNvoiStack      = "nvoi/stack"
	LabelNvoiRole       = "nvoi-role"
	LabelConfigChecksum = "nvoi/config-checksum"
	LabelNvoiDeployHash = "nvoi/deploy-hash"
	// LabelNvoiDatabase marks every k8s object owned by the databases
	// pipeline (StatefulSet, Service, PVC, credentials Secret, backup
	// CronJob, branch resources). Stamped by the postgres provider and
	// by provider.BuildBackupCronJob. Consumers use it to distinguish
	// "user workload" from "infrastructure workload" — most importantly,
	// the orphan sweeps in reconcile.Services / reconcile.Crons exclude
	// these so a config without `crons:` doesn't garbage-collect the
	// daily backup CronJob.
	LabelNvoiDatabase = "nvoi/database"
	RoleMaster        = "master"
	RoleWorker        = "worker"
	RoleBuilder       = "builder"
)

// BuilderCacheMountPath is the on-disk mount point for the per-builder cache
// volume. Docker's data-root points here (see pkg/infra/cloudinit.go's
// RenderBuilderCloudInit) so buildkit layer cache survives reboots and
// short-circuits the second-and-later deploys.
const BuilderCacheMountPath = "/var/lib/nvoi/builder-cache"

// BuilderCacheVolumeSizeGB is the default size of each per-builder cache
// volume. 50 GB is enough to hold a healthy buildkit cache for several
// services across repeated deploys without resizing; under that, the cache
// churns. Hetzner, AWS EBS, and Scaleway BlockStorage all accept 50 GB.
// Not user-configurable today — cache size is an operator concern, not a
// workload concern; revisit when we see cache pressure in real usage.
const BuilderCacheVolumeSizeGB = 50

// ── Storage env naming ─────────────────────────────────────────────────────────

func StorageEnvPrefix(bucketName string) string {
	upper := strings.ToUpper(bucketName)
	return "STORAGE_" + strings.ReplaceAll(upper, "-", "_")
}

func DatabaseEnvName(name string) string {
	upper := strings.ToUpper(name)
	return "DATABASE_URL_" + strings.ReplaceAll(upper, "-", "_")
}

// ── Network CIDRs ──────────────────────────────────────────────────────────────

const (
	PrivateNetworkCIDR   = "10.0.0.0/16"
	PrivateNetworkSubnet = "10.0.1.0/24"
	K3sClusterCIDR       = "10.42.0.0/16"
	K3sServiceCIDR       = "10.43.0.0/16"
)

// ── Constants ──────────────────────────────────────────────────────────────────

const (
	DefaultUser  = "deploy"
	DefaultImage = "ubuntu-24.04"
	MasterKey    = "master"
)

// ── Volume parsing ─────────────────────────────────────────────────────────────

// ParseVolumeMount splits "pgdata:/var/lib/postgresql/data" → ("pgdata", "/var/lib/...", true, true).
func ParseVolumeMount(s string) (source, target string, named bool, ok bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", "", false, false
	}
	source = s[:i]
	rest := s[i+1:]
	if j := strings.Index(rest, ":"); j >= 0 {
		target = rest[:j]
	} else {
		target = rest
	}
	if source == "" {
		return "", "", false, false
	}
	named = source[0] != '/' && source[0] != '.'
	return source, target, named, true
}

// ── Name validation ───────────────────────────────────────────────────────────

// dns1123 matches valid DNS-1123 labels: lowercase alphanumeric + hyphens,
// no leading/trailing hyphen, max 63 chars.
var dns1123 = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateName checks that a name conforms to DNS-1123 label rules.
// Used for app, env, server, service, cron, volume, storage, and build names.
func ValidateName(field, value string) error {
	if !dns1123.MatchString(value) {
		return fmt.Errorf("%s %q is invalid — must be lowercase alphanumeric with hyphens, no leading/trailing hyphen, max 63 chars (e.g. my-app)", field, value)
	}
	return nil
}

// envVarName matches valid environment variable names: uppercase alphanumeric + underscores,
// must start with a letter. Standard POSIX.
var envVarName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// ValidateEnvVarName checks that a name is a valid environment variable name.
func ValidateEnvVarName(field, value string) error {
	if !envVarName.MatchString(value) {
		return fmt.Errorf("%s %q is invalid — must be uppercase alphanumeric with underscores, starting with a letter (e.g. JWT_SECRET)", field, value)
	}
	return nil
}

// validDomain matches valid domain names: labels separated by dots, each label
// lowercase alphanumeric + hyphens, no leading/trailing hyphen.
var validDomain = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidateDomain checks that a value is a valid domain name.
func ValidateDomain(field, value string) error {
	if !validDomain.MatchString(value) {
		return fmt.Errorf("%s %q is invalid — must be a valid domain name (e.g. example.com, api.example.com)", field, value)
	}
	return nil
}

// sanitize normalizes a name to DNS-1123 format.
// Used internally by NewNames — callers should validate first.
func sanitize(s string) string {
	s = strings.ToLower(s)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)
