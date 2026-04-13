package core

import (
	"fmt"
	"io"
	"os"

	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
	s3client "github.com/getnvoi/nvoi/pkg/utils/s3"
	"github.com/spf13/cobra"
)

func NewDatabaseCmd(dc *config.DeployContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "database",
		Aliases: []string{"db"},
		Short:   "Database operations",
	}

	var dbName string
	cmd.PersistentFlags().StringVar(&dbName, "name", "", "database name (defaults to first)")

	cmd.AddCommand(newDatabaseBackupCmd(dc, &dbName))
	cmd.AddCommand(newDatabaseSQLCmd(dc, &dbName))
	return cmd
}

func newDatabaseBackupCmd(dc *config.DeployContext, dbName *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage database backups",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "now",
		Short: "Trigger a backup immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ResolveDBName(cmd, dbName)
			cronName := name + "-db-backup"
			return app.CronRun(cmd.Context(), app.CronRunRequest{
				Cluster: dc.Cluster,
				Name:    cronName,
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List backups in bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ResolveDBName(cmd, dbName)
			return DatabaseBackupList(cmd, dc, name)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "download <backup-name>",
		Short: "Download a backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ResolveDBName(cmd, dbName)
			outFile, _ := cmd.Flags().GetString("file")
			return DatabaseBackupDownload(cmd, dc, name, args[0], outFile)
		},
	})
	cmd.Commands()[2].Flags().StringP("file", "f", "", "output file (default: stdout)")
	return cmd
}

func newDatabaseSQLCmd(dc *config.DeployContext, dbName *string) *cobra.Command {
	return &cobra.Command{
		Use:   "sql <query>",
		Short: "Run SQL against the database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ResolveDBName(cmd, dbName)
			return DatabaseSQL(cmd, dc, name, args[0])
		},
	}
}

func ResolveDBName(cmd *cobra.Command, dbName *string) string {
	if *dbName != "" {
		return *dbName
	}
	cfg, err := LoadConfig(cmd)
	if err != nil || len(cfg.Database) == 0 {
		return "main"
	}
	for name := range cfg.Database {
		return name
	}
	return "main"
}

func DatabaseBackupList(cmd *cobra.Command, dc *config.DeployContext, name string) error {
	ssh, names, err := dc.Cluster.SSH(cmd.Context())
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	bucketName := name + "-db-backups"
	secretName := names.KubeSecrets()

	endpoint, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "ENDPOINT"))
	bucket, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "BUCKET"))
	accessKey, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "ACCESS_KEY_ID"))
	secretKey, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "SECRET_ACCESS_KEY"))

	if endpoint == "" || bucket == "" {
		return fmt.Errorf("backup bucket credentials not found — has the database been deployed?")
	}

	objects, err := s3client.ListObjects(endpoint, accessKey, secretKey, bucket, "backups/")
	if err != nil {
		return fmt.Errorf("list backups: %w", err)
	}

	out := dc.Cluster.Log()
	out.Command("database", "backup list", name)
	if len(objects) == 0 {
		out.Info("no backups found")
		return nil
	}
	for _, obj := range objects {
		out.Info(fmt.Sprintf("%s  %s  %d bytes", obj.LastModified, obj.Key, obj.Size))
	}
	return nil
}

func DatabaseBackupDownload(cmd *cobra.Command, dc *config.DeployContext, name, backupKey, outFile string) error {
	ssh, names, err := dc.Cluster.SSH(cmd.Context())
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	bucketName := name + "-db-backups"
	secretName := names.KubeSecrets()

	endpoint, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "ENDPOINT"))
	bucket, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "BUCKET"))
	accessKey, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "ACCESS_KEY_ID"))
	secretKey, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, storageKey(bucketName, "SECRET_ACCESS_KEY"))

	if endpoint == "" || bucket == "" {
		return fmt.Errorf("backup bucket credentials not found")
	}

	key := backupKey
	if !hasPrefix(key, "backups/") {
		key = "backups/" + key
	}

	out := dc.Cluster.Log()
	out.Command("database", "backup download", key)

	body, _, _, err := s3client.GetStream(endpoint, accessKey, secretKey, bucket, key)
	if err != nil {
		return fmt.Errorf("download %s: %w", key, err)
	}
	defer body.Close()

	var w io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}

	n, err := io.Copy(w, body)
	if err != nil {
		return err
	}
	if outFile != "" {
		out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
	}
	return nil
}

func DatabaseSQL(cmd *cobra.Command, dc *config.DeployContext, name, query string) error {
	ssh, names, err := dc.Cluster.SSH(cmd.Context())
	if err != nil {
		return err
	}
	defer ssh.Close()

	ns := names.KubeNamespace()
	pod := name + "-db-0"
	secretName := name + "-db-credentials"
	prefix := utils.ToUpperSnake(name)

	user, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, prefix+"_POSTGRES_USER")
	dbname, _ := kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, prefix+"_POSTGRES_DB")

	if user == "" {
		// Try mysql
		user, _ = kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, prefix+"_MYSQL_USER")
		dbname, _ = kube.GetSecretValue(cmd.Context(), ssh, ns, secretName, prefix+"_MYSQL_DATABASE")
	}

	if user == "" {
		return fmt.Errorf("database credentials not found for %q", name)
	}

	sqlCmd := fmt.Sprintf("exec %s -- psql -U %s -d %s -c %q", pod, user, dbname, query)
	out, err := kube.RunKubectl(cmd.Context(), ssh, ns, sqlCmd)
	if err != nil {
		// Try mysql
		sqlCmd = fmt.Sprintf("exec %s -- mysql -u %s %s -e %q", pod, user, dbname, query)
		out, err = kube.RunKubectl(cmd.Context(), ssh, ns, sqlCmd)
		if err != nil {
			return fmt.Errorf("sql: %w", err)
		}
	}
	fmt.Print(string(out))
	return nil
}

func storageKey(bucketName, suffix string) string {
	upper := utils.ToUpperSnake(bucketName)
	return "STORAGE_" + upper + "_" + suffix
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
