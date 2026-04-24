package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/getnvoi/nvoi/internal/config"
	"github.com/getnvoi/nvoi/internal/reconcile"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/provider/postgres"
	"github.com/getnvoi/nvoi/pkg/utils"
	"github.com/spf13/cobra"
)

func newDatabaseCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "database",
		Short: "Run database operations",
	}
	cmd.AddCommand(newDatabaseSQLCmd(rt))
	cmd.AddCommand(newDatabaseBackupCmd(rt))
	cmd.AddCommand(newDatabaseRestoreCmd(rt))
	cmd.AddCommand(newDatabaseMigrateCmd(rt))
	cmd.AddCommand(newDatabaseSnapshotCmd(rt))
	cmd.AddCommand(newDatabaseSnapshotsCmd(rt))
	cmd.AddCommand(newDatabaseSnapshotDeleteCmd(rt))
	cmd.AddCommand(newDatabaseBranchCmd(rt))
	cmd.AddCommand(newDatabaseBranchDeleteCmd(rt))
	// TODO(#68, rollback): register a `rollback` subcommand here
	// (`nvoi database rollback <name> --to <snap>`) — in-place restore
	// of the primary DB from one of its own snapshots. Wraps the
	// future postgres.Rollback helper (see TODO in
	// pkg/provider/postgres/branching.go). Same Service, same DSN,
	// clients don't reconfigure. Wanted for "oops, jump backward 15
	// minutes" recovery scenarios; pre-shipping without a concrete
	// caller is speculative so it's deferred.
	return cmd
}

// newDatabaseBranchCmd creates a sibling database from a ZFS clone of
// the source DB's data. O(100ms) on the CSI side thanks to copy-on-
// write; writes to the branch diverge lazily, so disk cost starts at
// near-zero and grows with actual changes.
//
// The branch gets its own k8s Service (`{source-pvc}-br-{branch}`)
// and StatefulSet. Auth is inherited from the source's credentials
// Secret (ZFS clones carry the pg_roles table bit-exact). Dev tools
// connect to the branch Service instead of the primary.
func newDatabaseBranchCmd(rt *runtime) *cobra.Command {
	// TODO(#68, ttl): add `--ttl <duration>` flag (e.g. 7d). When set,
	// the branch gets `nvoi/branch-ttl=<RFC3339-expiry>` stamped on its
	// workloads, and the reconcile sweep (see TODO in
	// internal/reconcile/databases.go) garbage-collects it at expiry.
	// Load-bearing for per-PR preview workflows — without TTL, CI-
	// created branches accumulate until the zpool fills.
	return &cobra.Command{
		Use:   "branch <name> <branch-name>",
		Short: "Create a ZFS-cloned branch of a database",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseBranch(cmd, rt, args[0], args[1])
		},
	}
}

func newDatabaseBranchDeleteCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "branch-delete <name> <branch-name>",
		Short: "Delete a branch (StatefulSet + PVC + Service + VolumeSnapshot)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseBranchDelete(cmd, rt, args[0], args[1])
		},
	}
}

func runDatabaseBranch(cmd *cobra.Command, rt *runtime, dbName, branchName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	if def.Server == "" {
		return fmt.Errorf("databases.%s: branching requires a selfhosted engine — SaaS providers have their own native branching", dbName)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	masterSSH, _, err := rt.dc.Cluster.SSH(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master SSH: %w", err)
	}
	kc, _, cleanup, err := rt.dc.Cluster.Kube(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master kube: %w", err)
	}
	defer cleanup()

	version := def.Version
	if version == "" {
		version = "16"
	}
	src := postgres.BranchSource{
		Namespace:         names.KubeNamespace(),
		SourcePVC:         names.KubeDatabasePVC(dbName),
		SourceService:     names.Database(dbName),
		CredentialsSecret: names.KubeDatabaseCredentials(dbName),
		Size:              def.Size,
		Image:             "postgres:" + version + "-alpine",
		ServerRole:        def.Server,
	}

	res, err := postgres.Branch(cmd.Context(), kc, masterSSH, src, branchName)
	if err != nil {
		return err
	}
	fmt.Fprintf(rt.out.Writer(), "branch %s ready — service %s:5432 (snapshot %s)\n",
		res.Name, res.Service, res.SnapshotName)
	return nil
}

func runDatabaseBranchDelete(cmd *cobra.Command, rt *runtime, dbName, branchName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	if def.Server == "" {
		return fmt.Errorf("databases.%s: branching requires a selfhosted engine", dbName)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	masterSSH, _, err := rt.dc.Cluster.SSH(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master SSH: %w", err)
	}
	kc, _, cleanup, err := rt.dc.Cluster.Kube(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master kube: %w", err)
	}
	defer cleanup()
	return postgres.DeleteBranch(cmd.Context(), kc, masterSSH,
		names.KubeNamespace(), names.KubeDatabasePVC(dbName), branchName)
}

// newDatabaseSnapshotCmd creates a ZFS-backed snapshot of a database's
// PVC. Thin wrapper over postgres.Snapshot — the VolumeSnapshot object
// lands immediately, the CSI driver's controller runs `zfs snapshot`
// out-of-band (O(100ms) for typical pool sizes). Snapshots are the
// raw material for branch/rollback.
//
// SaaS engines (neon, planetscale) have their own native branching
// and don't go through this path — command refuses with a clear
// pointer to use the provider's UI.
func newDatabaseSnapshotCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot <name>",
		Short: "Create a ZFS snapshot of the database's PVC",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseSnapshot(cmd, rt, args[0])
		},
	}
	cmd.Flags().String("label", "", "human-readable label appended to the snapshot name (default: UTC timestamp)")
	return cmd
}

func newDatabaseSnapshotsCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshots <name>",
		Short: "List ZFS snapshots for a database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseSnapshots(cmd, rt, args[0])
		},
	}
}

func newDatabaseSnapshotDeleteCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot-delete <name> <snapshot-name>",
		Short: "Delete a ZFS snapshot",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseSnapshotDelete(cmd, rt, args[0], args[1])
		},
	}
}

func runDatabaseSnapshot(cmd *cobra.Command, rt *runtime, dbName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	if def.Server == "" {
		return fmt.Errorf("databases.%s: snapshots require a selfhosted engine — SaaS providers (neon, planetscale) use their own native branching", dbName)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	masterSSH, _, err := rt.dc.Cluster.SSH(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master SSH: %w", err)
	}
	label, _ := cmd.Flags().GetString("label")
	snapName, err := postgres.Snapshot(cmd.Context(), masterSSH,
		names.KubeNamespace(), names.KubeDatabasePVC(dbName), label)
	if err != nil {
		return err
	}
	fmt.Fprintf(rt.out.Writer(), "%s\n", snapName)
	return nil
}

func runDatabaseSnapshots(cmd *cobra.Command, rt *runtime, dbName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	if def.Server == "" {
		return fmt.Errorf("databases.%s: snapshots require a selfhosted engine", dbName)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	masterSSH, _, err := rt.dc.Cluster.SSH(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master SSH: %w", err)
	}
	snaps, err := postgres.ListSnapshots(cmd.Context(), masterSSH,
		names.KubeNamespace(), names.KubeDatabasePVC(dbName))
	if err != nil {
		return err
	}
	for _, s := range snaps {
		fmt.Fprintln(rt.out.Writer(), s)
	}
	return nil
}

func runDatabaseSnapshotDelete(cmd *cobra.Command, rt *runtime, dbName, snapName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	if def.Server == "" {
		return fmt.Errorf("databases.%s: snapshots require a selfhosted engine", dbName)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	masterSSH, _, err := rt.dc.Cluster.SSH(cmd.Context(), config.NewView(rt.cfg))
	if err != nil {
		return fmt.Errorf("master SSH: %w", err)
	}
	return postgres.DeleteSnapshot(cmd.Context(), masterSSH, names.KubeNamespace(), snapName)
}

func newDatabaseSQLCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <name> <sql>",
		Short: "Execute one SQL statement against a configured database",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbName := args[0]
			stmt := args[1]
			req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, dbName)
			if err != nil {
				return err
			}
			defer cleanup()
			defer prov.Close()

			res, err := prov.ExecSQL(cmd.Context(), req, stmt)
			if err != nil {
				return err
			}
			renderSQL(rt.out.Writer(), res)
			return nil
		},
	}
}

func newDatabaseBackupCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Run backup operations against a configured database",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "now <name>",
		Short: "Trigger a backup now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			defer prov.Close()
			ref, err := prov.BackupNow(cmd.Context(), req)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(rt.out.Writer(), "%s\n", ref.ID)
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list <name>",
		Short: "List backups",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			defer prov.Close()
			refs, err := prov.ListBackups(cmd.Context(), req)
			if err != nil {
				return err
			}
			for _, ref := range refs {
				if _, err := fmt.Fprintf(rt.out.Writer(), "%s\t%s\t%s\t%d\n", ref.ID, ref.Kind, ref.CreatedAt, ref.SizeBytes); err != nil {
					return err
				}
			}
			return nil
		},
	})
	download := &cobra.Command{
		Use:   "download <name> <backup-id>",
		Short: "Download a backup to stdout or a file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, args[0])
			if err != nil {
				return err
			}
			defer cleanup()
			defer prov.Close()
			outPath, _ := cmd.Flags().GetString("output")
			var w io.Writer = rt.out.Writer()
			if outPath != "" {
				f, err := os.Create(outPath)
				if err != nil {
					return err
				}
				defer f.Close()
				w = f
			}
			return prov.DownloadBackup(cmd.Context(), req, args[1], w)
		},
	}
	download.Flags().StringP("output", "o", "", "write backup to file instead of stdout")
	cmd.AddCommand(download)
	return cmd
}

// newDatabaseRestoreCmd replays a specific backup object into the
// current database. Works for every engine (postgres, mysql, neon,
// planetscale) via the unified restore Job launched by
// provider.RunRestoreJob — selfhosted DBs connect to the in-cluster
// Service, SaaS DBs connect to their external TLS endpoint, the Job
// doesn't care which.
//
// Destructive: replacing the live data with a backup loses any writes
// since the backup's timestamp. Requires --yes to skip confirmation.
func newDatabaseRestoreCmd(rt *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <name>",
		Short: "Replay a backup into the database (destructive)",
		Long: `Replay a backup artifact into the named database.

The backup is pulled from the bucket, gunzipped, and piped into the
engine's native restore tool against the database's DSN. Works for
selfhosted (postgres, mysql) and SaaS (neon, planetscale) engines —
the transport is the same: one-shot Job running the nvoi/db image.

Destructive: any writes since the backup's timestamp are lost. Pass
--yes to skip the confirmation prompt.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dbName := args[0]
			backupID, _ := cmd.Flags().GetString("from-backup")
			latest, _ := cmd.Flags().GetBool("latest")
			yes, _ := cmd.Flags().GetBool("yes")

			if backupID == "" && !latest {
				return fmt.Errorf("either --from-backup <id> or --latest is required")
			}
			if backupID != "" && latest {
				return fmt.Errorf("--from-backup and --latest are mutually exclusive")
			}

			req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, dbName)
			if err != nil {
				return err
			}
			defer cleanup()
			defer prov.Close()

			if req.Bucket == nil || req.BackupCredsSecretName == "" {
				return fmt.Errorf("databases.%s: restore requires backup: config on the database (providers.storage + backup schedule)", dbName)
			}

			if latest {
				refs, err := prov.ListBackups(cmd.Context(), req)
				if err != nil {
					return fmt.Errorf("list backups: %w", err)
				}
				if len(refs) == 0 {
					return fmt.Errorf("no backups available for databases.%s", dbName)
				}
				// ListBackups returns objects as stored; the most recent
				// is the lexicographically-largest key because keys are
				// `YYYYMMDDTHHMMSSZ.sql.gz` (ISO-ordered by design).
				backupID = refs[0].ID
				for _, r := range refs[1:] {
					if r.ID > backupID {
						backupID = r.ID
					}
				}
			}

			if !yes {
				fmt.Fprintf(rt.out.Writer(), "About to replay %q into databases.%s — this is destructive. Pass --yes to proceed.\n", backupID, dbName)
				return fmt.Errorf("confirmation required (pass --yes)")
			}

			if err := prov.Restore(cmd.Context(), req, backupID); err != nil {
				return err
			}
			fmt.Fprintf(rt.out.Writer(), "restored databases.%s from %s\n", dbName, backupID)
			return nil
		},
	}
	cmd.Flags().String("from-backup", "", "backup object key to replay (mutually exclusive with --latest)")
	cmd.Flags().Bool("latest", false, "replay the most recent backup")
	cmd.Flags().Bool("yes", false, "skip the destructive-action confirmation")
	return cmd
}

// newDatabaseMigrateCmd moves a database to the node declared in cfg
// by composing BackupNow → teardown old → apply new per cfg → restore
// → health-check. The apply step reuses reconcile.ReconcileOneDatabase
// so the shape of the recreated workloads is identical to what a
// normal `nvoi deploy` would produce.
//
// Only supported form: no flags. The operator edits databases.X.server:
// in nvoi.yaml, runs `nvoi deploy` (which provisions the new node and
// emits a pending-migration warning), then runs this command.
//
// SaaS engines (neon, planetscale) have no server: — this command
// refuses to run on them.
func newDatabaseMigrateCmd(rt *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate <name>",
		Short: "Move a database to the node declared in nvoi.yaml (destructive)",
		Long: `Move a database to the node named by databases.X.server: in nvoi.yaml.

Workflow:
  1. Edit databases.X.server: in nvoi.yaml to the target node.
  2. nvoi deploy              — provisions the new node, warns about pending migration.
  3. nvoi database migrate X  — this command. Takes a backup, tears down the old
                                StatefulSet + PVC, applies the new StatefulSet per
                                cfg, restores from the backup, health-checks.
  4. nvoi deploy              — (optional) verify clean convergence.

Downtime is the dump+restore window. Idempotent — re-running after success is a
no-op.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDatabaseMigrate(cmd, rt, args[0])
		},
	}
}

func runDatabaseMigrate(cmd *cobra.Command, rt *runtime, dbName string) error {
	def, ok := rt.cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not defined", dbName)
	}
	// SaaS engines have no in-cluster StatefulSet to migrate from; their
	// DR path is `restore --from-backup <id>`, not migrate.
	if def.Server == "" {
		return fmt.Errorf("databases.%s: migrate requires server: — SaaS engines (neon, planetscale) don't have a node to migrate between", dbName)
	}

	req, prov, cleanup, err := resolveDatabaseCommand(cmd, rt, dbName)
	if err != nil {
		return err
	}
	defer cleanup()
	defer prov.Close()

	if req.Bucket == nil || req.BackupCredsSecretName == "" {
		return fmt.Errorf("databases.%s: migrate requires backup: config (providers.storage + backup schedule). Without a bucket there's nowhere to stage the data during the move", dbName)
	}

	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return err
	}
	kc := rt.dc.Cluster.MasterKube
	if kc == nil {
		return fmt.Errorf("kube client required for migrate — are you connected to the cluster?")
	}

	existing, err := kc.GetStatefulSet(cmd.Context(), names.KubeNamespace(), names.Database(dbName))
	if err != nil {
		return fmt.Errorf("read live StatefulSet: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("databases.%s: no live StatefulSet found — nothing to migrate (run `nvoi deploy` first)", dbName)
	}
	currentNode := existing.Spec.Template.Spec.NodeSelector[utils.LabelNvoiRole]
	if currentNode == def.Server {
		fmt.Fprintf(rt.out.Writer(), "databases.%s: already on %s — no migration needed\n", dbName, def.Server)
		return nil
	}

	rt.out.Progress(fmt.Sprintf("migrating databases.%s: %s → %s", dbName, currentNode, def.Server))

	// 1. Fresh backup. The CronJob's template handles everything —
	// ENGINE, DSN, bucket creds — so the backup is identical to a
	// scheduled one. BackupNow returns the Job name; we then wait for
	// the Job to Complete. The backup KEY (S3 object name) is
	// deterministic: timestamp-formatted by the image.
	rt.out.Progress("backup: starting")
	backupRef, err := prov.BackupNow(cmd.Context(), req)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := kc.WaitForJob(cmd.Context(), req.Namespace, backupRef.ID, rt.out); err != nil {
		return fmt.Errorf("backup job %s: %w", backupRef.ID, err)
	}
	// Resolve the freshly-uploaded object key — the most recent object
	// in the bucket at this point is guaranteed to be the one we just
	// pushed (scheduled backups don't race because we just ran Now,
	// and migrate is single-operator).
	freshKey, err := latestBackupKey(cmd.Context(), prov, req)
	if err != nil {
		return fmt.Errorf("locate fresh backup: %w", err)
	}
	rt.out.Success(fmt.Sprintf("backup: %s", freshKey))

	// 2. Teardown old. DeleteByName removes Deployment/StatefulSet/
	// Service of the same base name; we also explicitly drop the PVC
	// so the old node's data volume is reclaimed (local-path hostPath
	// today, ZFS dataset per #68). The credentials Secret + backup-
	// creds Secret stay in place — the new pod will reuse them.
	rt.out.Progress("teardown: old node")
	if err := kc.DeleteByName(cmd.Context(), req.Namespace, names.Database(dbName)); err != nil {
		return fmt.Errorf("teardown old workloads: %w", err)
	}
	if err := kc.DeletePVC(cmd.Context(), req.Namespace, names.KubeDatabasePVC(dbName)); err != nil {
		return fmt.Errorf("teardown old pvc: %w", err)
	}

	// 3. Apply new per cfg. Reuses the normal reconcile pipeline (bucket
	// ensure, backup-creds Secret ensure, credentials Secret ensure,
	// provider.Reconcile → apply). The node selector now points at the
	// new server; the PVC binds on that node's storage class.
	rt.out.Progress(fmt.Sprintf("apply: new node %s", def.Server))
	sources, err := commandSources(rt)
	if err != nil {
		return err
	}
	if _, err := reconcile.ReconcileOneDatabase(cmd.Context(), rt.dc, rt.cfg, names, dbName, def, sources); err != nil {
		return fmt.Errorf("apply on %s: %w", def.Server, err)
	}

	// 4. Wait for the new pod to be Ready before restoring. Postgres
	// container initializes the PGDATA directory + the empty DB on
	// first boot; trying to restore before that's done will fail.
	rt.out.Progress("waiting for new pod ready")
	if err := kc.WaitRollout(cmd.Context(), req.Namespace, names.Database(dbName), "StatefulSet", false, rt.out); err != nil {
		return fmt.Errorf("new pod rollout: %w", err)
	}

	// 5. Restore. Same unified Job the standalone `restore` verb uses —
	// the migrate command is literally a composer over it.
	rt.out.Progress(fmt.Sprintf("restore: replaying %s", freshKey))
	// Refresh req.Kube since resolveDatabaseCommand built req earlier
	// with the kube client. Still valid — cleanup is deferred to end
	// of the command.
	if err := prov.Restore(cmd.Context(), req, freshKey); err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	// 6. Health check. `SELECT 1` is the simplest possible connectivity
	// + sanity probe. If it returns without error, the new pod is
	// serving and the data is intact enough for the engine to respond.
	if _, err := prov.ExecSQL(cmd.Context(), req, "SELECT 1"); err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	rt.out.Success(fmt.Sprintf("databases.%s migrated to %s", dbName, def.Server))
	return nil
}

// latestBackupKey returns the lexicographically-largest backup key,
// which is also the most recent because the image names objects as
// `YYYYMMDDTHHMMSSZ.sql.gz`. Used by migrate after BackupNow completes.
func latestBackupKey(ctx context.Context, prov provider.DatabaseProvider, req provider.DatabaseRequest) (string, error) {
	refs, err := prov.ListBackups(ctx, req)
	if err != nil {
		return "", err
	}
	if len(refs) == 0 {
		return "", fmt.Errorf("no backup objects found in bucket after BackupNow")
	}
	latest := refs[0].ID
	for _, r := range refs[1:] {
		if r.ID > latest {
			latest = r.ID
		}
	}
	return latest, nil
}

func resolveDatabaseCommand(cmd *cobra.Command, rt *runtime, name string) (provider.DatabaseRequest, provider.DatabaseProvider, func(), error) {
	def, ok := rt.cfg.Databases[name]
	if !ok {
		return provider.DatabaseRequest{}, nil, nil, fmt.Errorf("database %q is not defined", name)
	}
	names, err := rt.dc.Cluster.Names()
	if err != nil {
		return provider.DatabaseRequest{}, nil, nil, err
	}

	sources, err := commandSources(rt)
	if err != nil {
		return provider.DatabaseRequest{}, nil, nil, err
	}
	req, err := commandDatabaseRequest(name, def, names, sources)
	if err != nil {
		return provider.DatabaseRequest{}, nil, nil, err
	}
	req.Namespace = names.KubeNamespace()
	req.Labels = names.Labels()
	req.Log = rt.out
	// Backup bucket: same derivation as reconcile (one bucket per
	// database, deterministic name from Names.KubeDatabaseBackupBucket).
	// We don't EnsureBucket here — deploy is the only path that
	// provisions — but we do fetch credentials so list/download work.
	// If providers.storage is unset, the validator will have already
	// rejected any backup config; leaving Bucket nil for databases that
	// don't declare backup: is correct.
	if def.Backup != nil {
		creds, err := commandBucketCredentials(rt)
		if err != nil {
			return provider.DatabaseRequest{}, nil, nil, err
		}
		req.Bucket = &provider.BucketHandle{
			Name:        names.KubeDatabaseBackupBucket(name),
			Credentials: creds,
		}
		req.BackupCredsSecretName = names.KubeDatabaseBackupCreds(name)
	}

	kc, _, cleanup, kerr := rt.dc.Cluster.Kube(cmd.Context(), config.NewView(rt.cfg))
	if kerr == nil {
		req.Kube = kc
	} else {
		cleanup = func() {}
	}

	creds, err := resolveProviderCreds(rt.dc.Creds, "database", def.Engine)
	if err != nil {
		cleanup()
		return provider.DatabaseRequest{}, nil, nil, err
	}
	prov, err := provider.ResolveDatabase(def.Engine, creds)
	if err != nil {
		cleanup()
		return provider.DatabaseRequest{}, nil, nil, err
	}
	return req, prov, cleanup, nil
}

func commandBucketCredentials(rt *runtime) (provider.BucketCredentials, error) {
	if rt.dc.Storage.Name == "" {
		return provider.BucketCredentials{}, fmt.Errorf("providers.storage is required for database backups")
	}
	bucket, err := provider.ResolveBucket(rt.dc.Storage.Name, rt.dc.Storage.Creds)
	if err != nil {
		return provider.BucketCredentials{}, err
	}
	return bucket.Credentials(context.Background())
}

func commandSources(rt *runtime) (map[string]string, error) {
	secretValues, err := collectCommandSecrets(rt.cfg, rt.dc.Creds)
	if err != nil {
		return nil, err
	}
	return secretValues, nil
}

func collectCommandSecrets(cfg *config.AppConfig, source provider.CredentialSource) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range cfg.Secrets {
		v, err := source.Get(key)
		if err != nil {
			return nil, err
		}
		if v != "" {
			out[key] = v
		}
	}
	for _, db := range cfg.Databases {
		// user / password / database all support $VAR references (same
		// as the reconcile path). Missing `database` from this loop
		// would silently drop the DSN's dbname when cmd/ calls
		// commandDatabaseRequest, producing a broken req.Spec.Database.
		for _, raw := range []string{db.User, db.Password, db.Database} {
			for _, key := range utils.ExtractVarRefs(raw) {
				v, err := source.Get(key)
				if err != nil {
					return nil, err
				}
				if v != "" {
					out[key] = v
				}
			}
		}
	}
	return out, nil
}

func commandDatabaseRequest(name string, def config.DatabaseDef, names *utils.Names, sources map[string]string) (provider.DatabaseRequest, error) {
	req := provider.DatabaseRequest{
		Name:                  name,
		FullName:              names.Database(name),
		PodName:               names.KubeDatabasePod(name),
		PVCName:               names.KubeDatabasePVC(name),
		BackupName:            names.KubeDatabaseBackupCron(name),
		CredentialsSecretName: names.KubeDatabaseCredentials(name),
		Spec: provider.DatabaseSpec{
			Engine:  def.Engine,
			Version: def.Version,
			Server:  def.Server,
			Size:    def.Size,
			Region:  def.Region,
		},
	}
	// user / password / database — same lockstep as the reconcile path
	// (see internal/reconcile/databases.go::databaseRequest). Without
	// resolving def.Database here, `$MAIN_POSTGRES_DB` would reach the
	// provider unresolved and the DSN would target a non-existent DB.
	if def.User != "" {
		v, err := commandResolveRef(def.User, sources)
		if err != nil {
			return req, fmt.Errorf("databases.%s.user: %w", name, err)
		}
		req.Spec.User = v
	}
	if def.Password != "" {
		v, err := commandResolveRef(def.Password, sources)
		if err != nil {
			return req, fmt.Errorf("databases.%s.password: %w", name, err)
		}
		req.Spec.Password = v
	}
	if def.Database != "" {
		v, err := commandResolveRef(def.Database, sources)
		if err != nil {
			return req, fmt.Errorf("databases.%s.database: %w", name, err)
		}
		req.Spec.Database = v
	}
	if def.Backup != nil {
		req.Spec.Backup = &provider.DatabaseBackupSpec{
			Schedule:  def.Backup.Schedule,
			Retention: def.Backup.Retention,
		}
	}
	return req, nil
}

func commandResolveRef(raw string, sources map[string]string) (string, error) {
	if !utils.HasVarRef(raw) {
		return raw, nil
	}
	keys := utils.ExtractVarRefs(raw)
	if len(keys) != 1 {
		return "", fmt.Errorf("multiple $VAR references are not supported in this field")
	}
	v, ok := sources[keys[0]]
	if !ok {
		return "", fmt.Errorf("$%s is not a known env var", keys[0])
	}
	return v, nil
}

func renderSQL(w io.Writer, res *provider.SQLResult) {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	if len(res.Columns) > 0 {
		_, _ = fmt.Fprintln(tw, strings.Join(res.Columns, "\t"))
	}
	for _, row := range res.Rows {
		_, _ = fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(w, "(%s rows)\n", strconv.FormatInt(res.RowsAffected, 10))
}
