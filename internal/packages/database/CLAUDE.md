# CLAUDE.md — internal/packages/database

The database package bundles a PostgreSQL or MySQL StatefulSet, headless Service, credentials Secret, backup S3 bucket, and backup CronJob from a single `database:` config block.

## Implicit behaviors

### Server pinning through volume

The database has no `server` field. It inherits its server from its volume:

```
database.bugsink.volume: bugsinkdata
  -> volumes.bugsinkdata.server: bugsink
    -> StatefulSet nodeSelector: {"nvoi/role": "bugsink"}
```

`manifests.go` reads `cfg.Volumes[db.Volume].Server` and sets `NodeSelector` on the StatefulSet. Kubernetes enforces placement — the pod cannot schedule on any other node. This is intentional: the database uses host-path storage, so it must run on the same server as its volume. The volume declaration is the single source of truth for placement.

### HostPath uses the resolved volume mount path

The StatefulSet's HostPath is `db.VolumeMountPath` — resolved once in `config.Resolve()` from the actual volume config key. `manifests.go` receives it as a parameter and uses it verbatim. It never derives a path from the database name. This is tested: `manifests_test.go` parses the generated YAML back into a StatefulSet and asserts HostPath == the exact input.

### Backup CronJob runs on master

Backup CronJobs always run on the master node (`NodeSelector: {"nvoi/role": "master"}`), regardless of which server the database runs on. The backup connects to the database over the k8s network via the headless Service, dumps to stdout, and pipes to S3. It doesn't need host-path access.

### Backup credentials live in a per-cron secret

Backup bucket credentials are stored in `db.BackupCredSecret` (= `{name}-db-backup-secrets`), NOT in the global "secrets" k8s Secret. The backup CronJob references this secret via `secretKeyRef` env vars. CLI commands (`nvoi db backup list/download`) read from the same secret via `utils.DatabaseBackupCredsSecret()`.

### Credentials are user-owned

No auto-generation. The database package reads credentials from `DeployContext.DatabaseCreds`, which are resolved at the OS boundary (`cmd/cli/local.go` reads env vars, `cmd/api/` reads from the database). Missing credentials = hard error with guidance on which env var to set.

Required env vars for a database named `main` with postgres:
```
MAIN_POSTGRES_USER, MAIN_POSTGRES_PASSWORD, MAIN_POSTGRES_DB
```

### Env var injection

After reconciliation, the package returns env vars that get injected into all app services:

```
{PREFIX}_DATABASE_URL          -- full connection string
{PREFIX}_POSTGRES_USER         -- username
{PREFIX}_POSTGRES_PASSWORD     -- password
{PREFIX}_POSTGRES_DB           -- database name
{PREFIX}_POSTGRES_HOST         -- headless service name ({name}-db)
{PREFIX}_POSTGRES_PORT         -- engine default port (5432/3306)
```

`{PREFIX}` = uppercase database config key (e.g., `MAIN`, `BUGSINK`).

These are stored in a per-database k8s Secret (`db.SecretName`), and also returned as a map so the reconciler can inject them into service manifests as `secretKeyRef` entries.

## Derived resource names

Every database config key produces 6 k8s resource identifiers. These are defined once in `pkg/utils/naming.go` and resolved once in `config.Resolve()` into `DatabaseDef` fields. No code outside these two places derives database names.

| Identifier | Resource | `DatabaseDef` field | `utils` function |
|---|---|---|---|
| `{name}-db` | StatefulSet + Service + Pod | `ServiceName` | `DatabaseServiceName()` |
| `{name}-db-credentials` | K8s Secret (DB creds) | `SecretName` | `DatabaseSecretName()` |
| `{name}-db-backup` | Backup CronJob | `BackupCronName` | `DatabaseBackupCronName()` |
| `{name}-db-backups` | S3 backup bucket | `BackupBucket` | `DatabaseBackupBucket()` |
| `{name}-db-backup-secrets` | K8s Secret (backup bucket creds) | `BackupCredSecret` | `DatabaseBackupCredsSecret()` |
| `{name}-db-0` | StatefulSet pod (k8s convention) | — | `DatabasePodName()` |

Consumers read from `DatabaseDef` fields (deploy path) or `utils.Database*()` functions (CLI path where config isn't available). Never from string concatenation.

## Orphan protection

Package-managed resources are protected from orphan deletion. The reconcilers for services, crons, and storage each check `cfg.Database` and skip resources whose names match `db.ServiceName`, `db.BackupCronName`, `db.BackupBucket`. If these diverge from what the package creates, the reconciler deletes the database resources as orphans — that's data destruction, not a silent path mismatch.

## Storage discovery

`config.StorageNames()` is the single source of truth for "what storage exists" — it includes both user-declared storage (`cfg.Storage` keys) and database backup buckets (`db.BackupBucket` for each database). `StorageList()` and `Describe()` use this — they never scan k8s secrets.

## Files

| File | Purpose |
|------|---------|
| `database.go` | Package registration, Validate, Reconcile, Teardown |
| `manifests.go` | StatefulSet + headless Service YAML generation |
| `manifests_test.go` | Parse generated YAML, assert HostPath/names/secrets invariants |
| `backup.go` | Backup CronJob YAML generation |
| `credentials.go` | Read from DeployContext, store as k8s Secret |
| `engine.go` | Engine interface + Postgres/MySQL implementations |
| `engine_test.go` | Engine behavior tests |

## Engine interface

`Engine` abstracts database-specific behavior: port, connection URL format, container env vars, data directory, readiness probe, dump command, password env var, env var naming. `EngineFor(kind)` returns the implementation — panics on unknown kinds (validated at config time, never at runtime).
