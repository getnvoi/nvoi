package main

import "context"

// Backend dispatches CLI commands to the agent (SSH tunnel) or local bootstrap.
// Commands read flags and pass values — backends resolve the rest.
// dbName and engine can be empty; the backend fills in from config.
type Backend interface {
	Deploy(ctx context.Context) error
	Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error
	Describe(ctx context.Context, jsonOutput bool) error
	Resources(ctx context.Context, jsonOutput bool) error
	Logs(ctx context.Context, opts LogsOpts) error
	Exec(ctx context.Context, service string, command []string) error
	SSH(ctx context.Context, command []string) error
	CronRun(ctx context.Context, name string) error
	DatabaseBackupList(ctx context.Context, dbName string) error
	DatabaseBackupDownload(ctx context.Context, dbName, key, outFile string) error
	DatabaseSQL(ctx context.Context, dbName, engine, query string) error
}

// LogsOpts holds resolved flags for the logs command.
type LogsOpts struct {
	Service    string
	Follow     bool
	Tail       int
	Since      string
	Previous   bool
	Timestamps bool
}
