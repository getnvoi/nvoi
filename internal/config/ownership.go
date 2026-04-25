package config

import (
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// BuildOwnershipContext derives the per-kind expected-name sets that
// pkg/core.Classify uses to stamp the four-state Owned column on every
// row of `nvoi resources`. Single function — naming rules and cfg
// shape both live here.
//
// Returns nil when cfg is nil. Returns a context with empty AppEnv
// when cfg has malformed app/env (the validator catches that upstream;
// nil-equivalent here is a defensive fallback).
func BuildOwnershipContext(cfg *AppConfig) *provider.OwnershipContext {
	if cfg == nil {
		return nil
	}
	names, err := utils.NewNames(cfg.App, cfg.Env)
	if err != nil {
		return nil
	}

	ctx := &provider.OwnershipContext{
		ExpectedServers:   map[string]bool{},
		ExpectedVolumes:   map[string]bool{},
		ExpectedFirewalls: map[string]bool{},
		ExpectedNetworks:  map[string]bool{},
		ExpectedDNS:       map[string]bool{},
		ExpectedBuckets:   map[string]bool{},
		ExpectedTunnels:   map[string]bool{},
	}

	hasMaster, hasWorker, hasBuilder := false, false, false
	for key, srv := range cfg.Servers {
		ctx.ExpectedServers[names.Server(key)] = true
		switch srv.Role {
		case utils.RoleWorker:
			hasWorker = true
		case utils.RoleBuilder:
			hasBuilder = true
		default:
			hasMaster = true
		}
	}
	if hasMaster {
		ctx.ExpectedFirewalls[names.MasterFirewall()] = true
	}
	if hasWorker {
		ctx.ExpectedFirewalls[names.WorkerFirewall()] = true
	}
	if hasBuilder {
		ctx.ExpectedFirewalls[names.BuilderFirewall()] = true
	}
	if len(cfg.Servers) > 0 {
		ctx.ExpectedNetworks[names.Network()] = true
	}

	for key := range cfg.Volumes {
		ctx.ExpectedVolumes[names.Volume(key)] = true
	}
	for key, srv := range cfg.Servers {
		if srv.Role == utils.RoleBuilder {
			ctx.ExpectedVolumes[names.BuilderCacheVolume(key)] = true
		}
	}

	for _, domains := range cfg.Domains {
		for _, d := range domains {
			ctx.ExpectedDNS[d] = true
		}
	}

	for key := range cfg.Storage {
		ctx.ExpectedBuckets[names.Bucket(key)] = true
	}
	for key, db := range cfg.Databases {
		if db.Backup != nil {
			ctx.ExpectedBuckets[names.KubeDatabaseBackupBucket(key)] = true
		}
	}

	if cfg.Providers.Tunnel != "" {
		ctx.ExpectedTunnels[names.Base()] = true
	}

	return ctx
}
