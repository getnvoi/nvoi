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
	"github.com/getnvoi/nvoi/pkg/provider"
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
	return cmd
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
		for _, raw := range []string{db.User, db.Password} {
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
			Engine:   def.Engine,
			Version:  def.Version,
			Server:   def.Server,
			Size:     def.Size,
			Database: def.Database,
			Region:   def.Region,
		},
	}
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
