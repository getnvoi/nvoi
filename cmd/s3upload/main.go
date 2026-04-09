// s3upload reads stdin and uploads to S3-compatible storage.
// Used by database backup CronJobs to pipe pg_dump output to storage.
//
// Required env vars (injected by --storage on cron.set):
//
//	STORAGE_ENDPOINT
//	STORAGE_BUCKET
//	STORAGE_ACCESS_KEY_ID
//	STORAGE_SECRET_ACCESS_KEY
//
// Optional:
//
//	STORAGE_KEY_PREFIX  — prefix for the object key (default: "")
//
// The object key is: {prefix}{timestamp}.sql.gz
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils/s3"
)

func main() {
	endpoint := requireEnv("STORAGE_ENDPOINT")
	bucket := requireEnv("STORAGE_BUCKET")
	accessKey := requireEnv("STORAGE_ACCESS_KEY_ID")
	secretKey := requireEnv("STORAGE_SECRET_ACCESS_KEY")
	prefix := os.Getenv("STORAGE_KEY_PREFIX")

	key := prefix + time.Now().UTC().Format("2006-01-02-150405") + ".sql.gz"

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatal("read stdin: %v", err)
	}
	if len(data) == 0 {
		fatal("stdin is empty — nothing to upload")
	}

	if err := s3.Put(endpoint, accessKey, secretKey, bucket, key, data); err != nil {
		fatal("upload %s: %v", key, err)
	}

	fmt.Fprintf(os.Stderr, "uploaded %s (%d bytes)\n", key, len(data))
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatal("missing required env var %s", key)
	}
	return v
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "s3upload: "+format+"\n", args...)
	os.Exit(1)
}
