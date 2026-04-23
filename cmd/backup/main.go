// cmd/backup is the entrypoint for the `docker.io/nvoi/backup` image —
// the uniform database backup runner every DatabaseProvider's CronJob
// invokes.
//
// Contract:
//
//	ENV (injected by the CronJob — see provider.BuildBackupCronJob):
//	  ENGINE              postgres | mysql | neon | planetscale
//	  DATABASE_NAME       logical name (the YAML key, e.g. "app")
//	  DATABASE_FULL_NAME  nvoi-{app}-{env}-db-{name}
//	  DB_URL              DSN — sourced from the database credentials
//	                      Secret's `url` key, prefixed `DB_` by envFrom
//	  BUCKET_ENDPOINT     S3-compatible base URL
//	  BUCKET_NAME         target bucket (one-per-database)
//	  AWS_ACCESS_KEY_ID   sigv4 signing key
//	  AWS_SECRET_ACCESS_KEY
//	  AWS_REGION          S3 region ("auto" for R2)
//
// Pipeline:
//
//  1. Pick dump tool (pg_dump for postgres/neon; mysqldump for mysql/
//     planetscale). SaaS engines dump against the external endpoint over
//     TLS — same tool, different DSN.
//  2. Stream dump → gzip → temp file at /tmp/backup.sql.gz.
//  3. Stat the file for content-length.
//  4. PUT to s3://$BUCKET_NAME/<YYYYMMDDTHHMMSSZ>.sql.gz via sigv4 with
//     UNSIGNED-PAYLOAD (keeps memory flat for large dumps).
//  5. Delete the temp file; exit 0 on success, non-zero + stderr on
//     failure so the k8s Job records Failed and Retries per BackoffLimit.
//
// Uniformity is load-bearing — list/download are bucket-level (shared
// across every provider), so the artifact layout MUST be identical:
// gzipped logical dump, one per object, timestamp-named, flat key.
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
		fmt.Fprintf(os.Stderr, "nvoi-backup: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
		fmt.Fprintf(os.Stderr, "nvoi-backup: missing required env var %s\n", key)
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
