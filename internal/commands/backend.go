// Package commands defines the shared command tree used by both the direct CLI
// and the cloud CLI. Each CLI provides a Backend implementation — DirectBackend
// executes immediately via pkg/core, CloudBackend mutates config and calls the API.
package commands

import "context"

// Backend is the contract between shared commands and their execution strategy.
// DirectBackend calls pkg/core/ functions. CloudBackend mutates config + API.
type Backend interface {
	// Infrastructure
	InstanceSet(ctx context.Context, name, serverType, region, role string) error
	InstanceDelete(ctx context.Context, name string) error
	InstanceList(ctx context.Context) error
	FirewallSet(ctx context.Context, args []string) error
	FirewallList(ctx context.Context) error
	VolumeSet(ctx context.Context, name string, size int, server string) error
	VolumeDelete(ctx context.Context, name string) error
	VolumeList(ctx context.Context) error

	// Storage
	StorageSet(ctx context.Context, name, bucket string, cors bool, expireDays int) error
	StorageDelete(ctx context.Context, name string) error
	StorageEmpty(ctx context.Context, name string) error
	StorageList(ctx context.Context) error

	// Application
	ServiceSet(ctx context.Context, name string, opts ServiceOpts) error
	ServiceDelete(ctx context.Context, name string) error
	CronSet(ctx context.Context, name string, opts CronOpts) error
	CronDelete(ctx context.Context, name string) error

	// Managed
	DatabaseSet(ctx context.Context, name string, opts ManagedOpts) error
	DatabaseDelete(ctx context.Context, name, kind string) error
	DatabaseList(ctx context.Context) error
	BackupCreate(ctx context.Context, name, kind string) error
	BackupList(ctx context.Context, name, kind, backupStorage string) error
	BackupDownload(ctx context.Context, name, kind, backupStorage, key string) error
	AgentSet(ctx context.Context, name string, opts ManagedOpts) error
	AgentDelete(ctx context.Context, name, kind string) error
	AgentList(ctx context.Context) error
	AgentExec(ctx context.Context, name, kind string, command []string) error
	AgentLogs(ctx context.Context, name, kind string, opts LogsOpts) error

	// Secrets
	SecretSet(ctx context.Context, key, value string) error
	SecretDelete(ctx context.Context, key string) error
	SecretList(ctx context.Context) error
	SecretReveal(ctx context.Context, key string) (string, error)

	// Build
	Build(ctx context.Context, opts BuildOpts) error
	BuildList(ctx context.Context) error
	BuildLatest(ctx context.Context, name string) (string, error)
	BuildPrune(ctx context.Context, name string, keep int) error

	// DNS + Ingress
	DNSSet(ctx context.Context, routes []RouteArg, cloudflareManaged bool) error
	DNSDelete(ctx context.Context, routes []RouteArg) error
	DNSList(ctx context.Context) error
	IngressSet(ctx context.Context, routes []RouteArg, cloudflareManaged bool, certPEM, keyPEM string) error
	IngressDelete(ctx context.Context, routes []RouteArg, cloudflareManaged bool) error

	// Operational
	Describe(ctx context.Context, jsonOutput bool) error
	Resources(ctx context.Context, jsonOutput bool) error
	Logs(ctx context.Context, service string, opts LogsOpts) error
	Exec(ctx context.Context, service string, command []string) error
	SSH(ctx context.Context, command []string) error
}

// WorkloadOpts holds the flags shared by service set and cron set.
// Both workload types run containers with the same image/env/secret/storage/volume model.
type WorkloadOpts struct {
	Image   string
	Command string
	Server  string
	Env     []string
	Secrets []string
	Storage []string
	Volumes []string
}

// ServiceOpts holds all flags for service set.
type ServiceOpts struct {
	WorkloadOpts
	Port     int
	Replicas int
	Health   string
	NoWait   bool
}

// CronOpts holds all flags for cron set.
type CronOpts struct {
	WorkloadOpts
	Schedule string
}

// ManagedOpts holds flags for managed service commands (database set, agent set).
type ManagedOpts struct {
	Kind          string
	Secrets       []string
	Image         string
	VolumeSize    int
	BackupStorage string
	BackupCron    string
}

// BuildOpts holds all flags for build.
type BuildOpts struct {
	Targets      []string // name:source pairs
	Branch       string
	Platform     string
	Architecture string
	History      int
}

// LogsOpts holds all flags for log viewing.
type LogsOpts struct {
	Follow     bool
	Tail       int
	Since      string
	Previous   bool
	Timestamps bool
}

// RouteArg represents a parsed service:domain,domain argument.
type RouteArg struct {
	Service string
	Domains []string
}
