// cmd/db is the entrypoint for the `docker.io/nvoi/db` image — the
// uniform database backup AND restore runner every DatabaseProvider's
// CronJob / Job invokes. (Previously cmd/backup — renamed once restore
// landed because the old name stopped describing the behavior.)
//
// Contract:
//
//	ENV (injected by the CronJob — see provider.BuildBackupCronJob,
//	     or the Job — see provider.BuildRestoreJob):
//	  MODE                backup (default) | restore
//	  ENGINE              postgres | mysql | neon | planetscale
//	  DATABASE_NAME       logical name (the YAML key, e.g. "app")
//	  DATABASE_FULL_NAME  nvoi-{app}-{env}-db-{name}
//	  DB_URL              DSN — sourced from the database credentials
//	                      Secret's `url` key, prefixed `DB_` by envFrom
//	  BUCKET_ENDPOINT     S3-compatible base URL
//	  BUCKET_NAME         target bucket (one-per-database)
//	  BACKUP_KEY          (restore mode only) S3 object key to replay
//	  AWS_ACCESS_KEY_ID   sigv4 signing key
//	  AWS_SECRET_ACCESS_KEY
//	  AWS_REGION          S3 region ("auto" for R2)
//
// Pipelines:
//
//	MODE=backup (default):
//	  1. Pick dump tool (pg_dump for postgres/neon; mysqldump for mysql/
//	     planetscale). SaaS engines dump against the external endpoint
//	     over TLS — same tool, different DSN.
//	  2. Stream dump → gzip → temp file at /tmp/backup.sql.gz.
//	  3. Stat the file for content-length.
//	  4. PUT to s3://$BUCKET_NAME/<YYYYMMDDTHHMMSSZ>.sql.gz via sigv4.
//	  5. Delete the temp file; exit 0 on success.
//
//	MODE=restore:
//	  1. s3.GetStream(BUCKET_NAME, BACKUP_KEY) → io.ReadCloser.
//	  2. Pipe through gzip.NewReader (decompression).
//	  3. Pipe into engine's restore tool (psql / mysql) against DB_URL.
//	  4. Exit 0 on success, non-zero + stderr on failure.
//
// Uniformity is load-bearing — the same image handles both directions,
// the same envFrom Secrets feed both, list/download are bucket-level.
// Every DatabaseProvider — selfhosted or SaaS — routes through here.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

const (
	tmpPath       = "/tmp/backup.sql.gz"
	uploadTimeout = 30 * time.Minute
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nvoi-db: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "backup"
	}
	switch mode {
	case "backup":
		return runBackup()
	case "restore":
		return runRestore()
	default:
		return fmt.Errorf("unknown MODE %q (expected: backup | restore)", mode)
	}
}

func runBackup() error {
	engine := mustEnv("ENGINE")
	dsn := mustEnv("DB_URL") // CronJob envFrom prefixes with DB_
	bucketEndpoint := mustEnv("BUCKET_ENDPOINT")
	bucketName := mustEnv("BUCKET_NAME")
	accessKey := mustEnv("AWS_ACCESS_KEY_ID")
	secretKey := mustEnv("AWS_SECRET_ACCESS_KEY")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "auto"
	}

	// 1) Pick dump tool.
	dumpCmd, err := dumpCommand(engine, dsn)
	if err != nil {
		return err
	}

	// 2) Stream dump → gzip → temp file. `pg_dump | gzip` keeps memory
	// flat; failures on either side propagate via the command's exit
	// code or gzip.Writer.Close().
	if err := runDumpToGzippedFile(dumpCmd, tmpPath); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	defer os.Remove(tmpPath)

	// 3) Stat for content-length.
	fi, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("stat dump: %w", err)
	}
	size := fi.Size()
	if size == 0 {
		return fmt.Errorf("dump is empty — refusing to upload a zero-byte backup (dump tool probably failed silently)")
	}

	// 4) Upload.
	key := time.Now().UTC().Format("20060102T150405Z") + ".sql.gz"
	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open dump: %w", err)
	}
	defer f.Close()

	if err := s3.PutStream(
		strings.TrimRight(bucketEndpoint, "/"),
		accessKey, secretKey, bucketName, key,
		f, size, uploadTimeout,
	); err != nil {
		return fmt.Errorf("upload %s/%s: %w", bucketName, key, err)
	}

	fmt.Printf("uploaded %s/%s (%d bytes, engine=%s)\n", bucketName, key, size, engine)
	return nil
}

// runRestore pulls a backup object from the bucket, gunzips, and pipes
// into the engine's native restore tool against $DB_URL. Same image,
// same envFrom Secrets as backup — just the direction flips. Works
// identically for selfhosted (in-cluster Service DSN) and SaaS (external
// TLS DSN) because the DSN is opaque to this tool.
//
// Exit discipline: the restore tool's exit code is the Job's exit code.
// ON_ERROR_STOP=1 (psql) / --force=false (mysql default) means the
// first SQL error stops the replay, so a partial restore fails loudly
// rather than leaving a half-populated database.
func runRestore() error {
	engine := mustEnv("ENGINE")
	dsn := mustEnv("DB_URL")
	bucketEndpoint := mustEnv("BUCKET_ENDPOINT")
	bucketName := mustEnv("BUCKET_NAME")
	backupKey := mustEnv("BACKUP_KEY")
	accessKey := mustEnv("AWS_ACCESS_KEY_ID")
	secretKey := mustEnv("AWS_SECRET_ACCESS_KEY")

	rc, _, _, err := s3.GetStream(
		strings.TrimRight(bucketEndpoint, "/"),
		accessKey, secretKey, bucketName, backupKey,
	)
	if err != nil {
		return fmt.Errorf("download %s/%s: %w", bucketName, backupKey, err)
	}
	defer rc.Close()

	gzr, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("gunzip %s/%s: %w", bucketName, backupKey, err)
	}
	defer gzr.Close()

	restoreCmd, err := restoreCommand(engine, dsn)
	if err != nil {
		return err
	}
	restoreCmd.Stdin = gzr
	restoreCmd.Stdout = os.Stdout
	restoreCmd.Stderr = os.Stderr
	if err := restoreCmd.Run(); err != nil {
		return fmt.Errorf("restore tool exited: %w", err)
	}
	fmt.Printf("restored %s/%s (engine=%s)\n", bucketName, backupKey, engine)
	return nil
}

// restoreCommand returns the exec.Cmd that reads SQL from stdin and
// applies it to the database at dsn. Mirrors dumpCommand in structure
// — the image owns tool selection, nvoi core does not.
//
// postgres / neon → `psql <DSN>` with ON_ERROR_STOP=1.
// mysql / planetscale → `mysql` with DSN parsed into -h/-u/-p flags
// (mysql's stdin-replay shape matches mysqldump's stdout-capture shape).
func restoreCommand(engine, dsn string) (*exec.Cmd, error) {
	switch engine {
	case "postgres", "neon":
		return exec.Command("psql", "-v", "ON_ERROR_STOP=1", dsn), nil
	case "mysql", "planetscale":
		host, user, password, db, err := parseMySQLDSN(dsn)
		if err != nil {
			return nil, err
		}
		args := []string{
			"--ssl-mode=REQUIRED",
			"-h", host,
			"-u", user,
			"--password=" + password,
			db,
		}
		return exec.Command("mysql", args...), nil
	default:
		return nil, fmt.Errorf("unknown ENGINE %q (expected: postgres | mysql | neon | planetscale)", engine)
	}
}

// dumpCommand returns the exec.Cmd that produces a SQL dump on stdout
// for the requested engine. Kept here (not in pkg/) because this is the
// image's contract — the image owns dump-tool selection; nvoi core does
// not.
//
// postgres / neon → `pg_dump <DSN>` (neon exposes a pg-wire endpoint).
// mysql / planetscale → `mysqldump` with DSN parsed into -h/-u/-p flags
// (mysqldump doesn't accept a single DSN URL the way pg_dump does).
func dumpCommand(engine, dsn string) (*exec.Cmd, error) {
	switch engine {
	case "postgres", "neon":
		return exec.Command("pg_dump", "--no-owner", "--no-acl", dsn), nil
	case "mysql", "planetscale":
		host, user, password, db, err := parseMySQLDSN(dsn)
		if err != nil {
			return nil, err
		}
		args := []string{
			"--single-transaction",
			"--set-gtid-purged=OFF",
			"-h", host,
			"-u", user,
			"--password=" + password,
			db,
		}
		// PlanetScale requires TLS — mysqldump doesn't enforce it by
		// default, so pass --ssl-mode=REQUIRED explicitly. Safe for
		// vanilla MySQL too (connects with TLS when the server offers
		// it; errors when it doesn't, which is the correct default for
		// a tool that exfiltrates data).
		args = append([]string{"--ssl-mode=REQUIRED"}, args...)
		return exec.Command("mysqldump", args...), nil
	default:
		return nil, fmt.Errorf("unknown ENGINE %q (expected: postgres | mysql | neon | planetscale)", engine)
	}
}

// parseMySQLDSN tears apart `mysql://user:pass@host[:port]/db[?…]` into
// the fields mysqldump wants as separate flags. Intentionally small —
// we don't pull in a mysql driver just to parse a URL.
func parseMySQLDSN(dsn string) (host, user, pass, db string, err error) {
	const prefix = "mysql://"
	if !strings.HasPrefix(dsn, prefix) {
		err = fmt.Errorf("mysql DSN must start with mysql:// (got %q)", shorten(dsn))
		return
	}
	rest := dsn[len(prefix):]

	// userinfo
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		err = fmt.Errorf("mysql DSN missing user@host separator")
		return
	}
	userinfo, rest := rest[:at], rest[at+1:]
	if i := strings.Index(userinfo, ":"); i >= 0 {
		user, pass = userinfo[:i], userinfo[i+1:]
	} else {
		user = userinfo
	}

	// strip query string
	if q := strings.Index(rest, "?"); q >= 0 {
		rest = rest[:q]
	}

	// host / db
	slash := strings.Index(rest, "/")
	if slash < 0 {
		err = fmt.Errorf("mysql DSN missing /database path")
		return
	}
	host, db = rest[:slash], rest[slash+1:]
	if host == "" || db == "" {
		err = fmt.Errorf("mysql DSN missing host or database (host=%q db=%q)", host, db)
		return
	}
	// Drop :port if present — mysqldump accepts `-h host:port` but
	// prefer the explicit -P flag if we ever need it. Today everyone's
	// on 3306.
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	return
}

// runDumpToGzippedFile runs the dump command, piping its stdout through
// gzip into the destination file. Stderr is forwarded so a failing
// dump surfaces its message in the k8s Job's pod logs.
func runDumpToGzippedFile(cmd *exec.Cmd, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.Copy(gz, stdout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("dump tool exited: %w", err)
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return out.Close()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "nvoi-db: missing required env var %s\n", key)
		os.Exit(1)
	}
	return v
}

// shorten redacts all but the scheme for error messages — DSNs contain
// credentials, and this tool runs under k8s logs the operator may share.
func shorten(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		return s[:i+3] + "…"
	}
	if len(s) > 8 {
		return s[:8] + "…"
	}
	return s
}
