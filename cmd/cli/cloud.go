package main

import (
	"context"
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/internal/cloud"
	app "github.com/getnvoi/nvoi/pkg/core"
)

// cloudBackend relays commands through the API via HTTP.
type cloudBackend struct {
	client     *cloud.APIClient
	repoPath   func(string) string
	out        app.Output
	configPath string // transitional — deploy/teardown send config until API stores it
}

func (b *cloudBackend) readConfig() ([]byte, error) {
	data, err := os.ReadFile(b.configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return data, nil
}

func (b *cloudBackend) Deploy(ctx context.Context) error {
	data, err := b.readConfig()
	if err != nil {
		return err
	}
	return cloud.StreamRun(b.client, b.repoPath("/deploy"), map[string]any{
		"config": string(data),
	})
}

func (b *cloudBackend) Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error {
	data, err := b.readConfig()
	if err != nil {
		return err
	}
	body := map[string]any{"config": string(data)}
	if deleteVolumes {
		body["delete_volumes"] = true
	}
	if deleteStorage {
		body["delete_storage"] = true
	}
	return cloud.StreamRun(b.client, b.repoPath("/teardown"), body)
}

func (b *cloudBackend) Describe(ctx context.Context, jsonOutput bool) error {
	return cloud.Describe(b.client, b.repoPath, jsonOutput)
}

func (b *cloudBackend) Resources(ctx context.Context, jsonOutput bool) error {
	return cloud.Resources(b.client, b.repoPath, jsonOutput)
}

func (b *cloudBackend) Logs(ctx context.Context, opts LogsOpts) error {
	return cloud.Logs(b.client, b.repoPath, cloud.LogsOpts(opts))
}

func (b *cloudBackend) Exec(ctx context.Context, service string, command []string) error {
	return cloud.Exec(b.client, b.repoPath, service, command)
}

func (b *cloudBackend) SSH(ctx context.Context, command []string) error {
	return cloud.SSH(b.client, b.repoPath, command)
}

func (b *cloudBackend) CronRun(ctx context.Context, name string) error {
	return cloud.StreamRun(b.client, b.repoPath("/cron/"+name+"/run"), nil)
}

// ── Database ────────────────────────────────────────────────────────────────
// Cloud mode: --name required (API could auto-resolve in the future).
// --kind required for sql (API could auto-resolve from stored config).

func (b *cloudBackend) requireDBName(dbName string) (string, error) {
	if dbName == "" {
		return "", fmt.Errorf("--name is required in cloud mode")
	}
	return dbName, nil
}

func (b *cloudBackend) DatabaseBackupList(ctx context.Context, dbName string) error {
	name, err := b.requireDBName(dbName)
	if err != nil {
		return err
	}
	return cloud.DatabaseBackupList(b.client, b.repoPath, b.out, name)
}

func (b *cloudBackend) DatabaseBackupDownload(ctx context.Context, dbName, key, outFile string) error {
	name, err := b.requireDBName(dbName)
	if err != nil {
		return err
	}
	return cloud.DatabaseBackupDownload(b.client, b.repoPath, b.out, name, key, outFile)
}

func (b *cloudBackend) DatabaseSQL(ctx context.Context, dbName, engine, query string) error {
	name, err := b.requireDBName(dbName)
	if err != nil {
		return err
	}
	if engine == "" {
		return fmt.Errorf("--kind is required in cloud mode (postgres or mysql)")
	}
	return cloud.DatabaseSQL(b.client, b.repoPath, name, engine, query)
}
