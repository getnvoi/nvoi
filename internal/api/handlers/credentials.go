package handlers

import (
	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// resolvedCredentials holds schema-mapped credentials for all provider kinds.
// Optional providers (DNS, Storage, Build) are nil when the provider is empty.
type resolvedCredentials struct {
	Compute map[string]string
	DNS     map[string]string
	Storage map[string]string
	Build   map[string]string
}

// resolveAllCredentials maps raw env vars to schema keys for every provider
// configured in the RepoConfig. Compute is required; DNS, Storage, and Build
// are optional (empty provider name means skip).
func resolveAllCredentials(rc *api.RepoConfig, env map[string]string) (*resolvedCredentials, error) {
	computeCreds, err := provider.MapComputeCredentials(string(rc.ComputeProvider), env)
	if err != nil {
		return nil, err
	}

	res := &resolvedCredentials{Compute: computeCreds}

	if rc.DNSProvider != "" {
		res.DNS, err = provider.MapDNSCredentials(string(rc.DNSProvider), env)
		if err != nil {
			return nil, err
		}
	}

	if rc.StorageProvider != "" {
		res.Storage, err = provider.MapBucketCredentials(string(rc.StorageProvider), env)
		if err != nil {
			return nil, err
		}
	}

	if rc.BuildProvider != "" {
		res.Build, err = provider.MapBuildCredentials(string(rc.BuildProvider), env)
		if err != nil {
			return nil, err
		}
	}

	return res, nil
}
