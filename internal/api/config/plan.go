package config

import (
	"fmt"
	"sort"
	"strings"
)

// StepKind identifies the pkg/core/ function to call.
type StepKind string

const (
	StepComputeSet    StepKind = "instance.set"
	StepComputeDelete StepKind = "instance.delete"
	StepVolumeSet     StepKind = "volume.set"
	StepVolumeDelete  StepKind = "volume.delete"
	StepBuild         StepKind = "build"
	StepSecretSet     StepKind = "secret.set"
	StepSecretDelete  StepKind = "secret.delete"
	StepStorageSet    StepKind = "storage.set"
	StepStorageDelete StepKind = "storage.delete"
	StepServiceSet    StepKind = "service.set"
	StepServiceDelete StepKind = "service.delete"
	StepDNSSet        StepKind = "dns.set"
	StepDNSDelete     StepKind = "dns.delete"
)

// Step is one action in the deploy plan.
// Maps 1:1 to a pkg/core/ function call.
type Step struct {
	Kind   StepKind       `json:"kind"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// Plan generates the ordered deploy sequence from a config + env.
// The sequence mirrors bin/deploy-* scripts:
//
//  1. Compute (servers)
//  2. Volumes
//  3. Build
//  4. Secrets
//  5. Storage
//  6. Services
//  7. DNS
//
// env is the parsed .env map — used to resolve secret values and
// env var references (entries without =).
func Plan(cfg *Config, env map[string]string) ([]Step, error) {
	if errs := Validate(cfg); len(errs) > 0 {
		return nil, errs[0]
	}

	var steps []Step

	// ── 1. Compute ─────────────────────────────────────────────────────────
	// First server is master, rest are workers. Deterministic order.
	serverNames := sortedKeys(cfg.Servers)
	for i, name := range serverNames {
		srv := cfg.Servers[name]
		params := map[string]any{
			"type":   srv.Type,
			"region": srv.Region,
		}
		if i > 0 {
			params["worker"] = true
		}
		steps = append(steps, Step{Kind: StepComputeSet, Name: name, Params: params})
	}

	// ── 2. Volumes ─────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Volumes) {
		vol := cfg.Volumes[name]
		steps = append(steps, Step{Kind: StepVolumeSet, Name: name, Params: map[string]any{
			"size":   vol.Size,
			"server": vol.Server,
		}})
	}

	// ── 3. Build ───────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Build) {
		b := cfg.Build[name]
		steps = append(steps, Step{Kind: StepBuild, Name: name, Params: map[string]any{
			"source": b.Source,
		}})
	}

	// ── 4. Secrets ─────────────────────────────────────────────────────────
	// Collect all secret keys referenced by any service. Dedupe + sort.
	secretKeys := collectSecrets(cfg)
	for _, key := range secretKeys {
		val, ok := env[key]
		if !ok {
			return nil, fmt.Errorf("secret %q referenced by service but not found in env", key)
		}
		steps = append(steps, Step{Kind: StepSecretSet, Name: key, Params: map[string]any{
			"value": val,
		}})
	}

	// ── 5. Storage ─────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Storage) {
		st := cfg.Storage[name]
		params := map[string]any{}
		if st.CORS {
			params["cors"] = true
		}
		if st.ExpireDays > 0 {
			params["expire_days"] = st.ExpireDays
		}
		if st.Bucket != "" {
			params["bucket"] = st.Bucket
		}
		steps = append(steps, Step{Kind: StepStorageSet, Name: name, Params: params})
	}

	// ── 6. Services ────────────────────────────────────────────────────────
	for _, name := range sortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		params := map[string]any{}

		if svc.Image != "" {
			params["image"] = svc.Image
		}
		if svc.Build != "" {
			params["build"] = svc.Build
		}
		if svc.Port > 0 {
			params["port"] = svc.Port
		}
		if svc.Replicas > 0 {
			params["replicas"] = svc.Replicas
		}
		if svc.Command != "" {
			params["command"] = svc.Command
		}
		if svc.Health != "" {
			params["health"] = svc.Health
		}
		if svc.Server != "" {
			params["server"] = svc.Server
		}
		if len(svc.Volumes) > 0 {
			params["volumes"] = svc.Volumes
		}
		if len(svc.Secrets) > 0 {
			params["secrets"] = svc.Secrets
		}
		if len(svc.Storage) > 0 {
			params["storage"] = svc.Storage
		}

		// Resolve env: KEY=VALUE stays literal, bare KEY resolves from .env.
		var resolvedEnv []string
		for _, entry := range svc.Env {
			if strings.Contains(entry, "=") {
				resolvedEnv = append(resolvedEnv, entry)
			} else {
				val, ok := env[entry]
				if !ok {
					return nil, fmt.Errorf("services.%s.env: %q not found in env", name, entry)
				}
				resolvedEnv = append(resolvedEnv, entry+"="+val)
			}
		}
		if len(resolvedEnv) > 0 {
			params["env"] = resolvedEnv
		}

		steps = append(steps, Step{Kind: StepServiceSet, Name: name, Params: params})
	}

	// ── 7. DNS ─────────────────────────────────────────────────────────────
	for _, svcName := range sortedKeys(cfg.Domains) {
		domains := cfg.Domains[svcName]
		steps = append(steps, Step{Kind: StepDNSSet, Name: svcName, Params: map[string]any{
			"domains": []string(domains),
		}})
	}

	return steps, nil
}

// collectSecrets deduplicates and sorts all secret key references across services.
// Handles both "KEY" and "ENV=KEY" formats — extracts the secret key (right side).
func collectSecrets(cfg *Config) []string {
	seen := map[string]bool{}
	for _, svc := range cfg.Services {
		for _, entry := range svc.Secrets {
			key := secretKey(entry)
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// secretKey extracts the k8s secret key from a secret entry.
// "POSTGRES_PASSWORD" → "POSTGRES_PASSWORD" (same name)
// "POSTGRES_PASSWORD=POSTGRES_PASSWORD_DB" → "POSTGRES_PASSWORD_DB" (aliased)
func secretKey(entry string) string {
	if _, key, ok := strings.Cut(entry, "="); ok {
		return key
	}
	return entry
}

// Diff generates delete steps for resources that existed in prev but are gone
// from current. Reverse order of deploy: DNS → services → storage → secrets →
// volumes → compute. Mirrors bin/destroy.
//
// prev may be nil (first deploy — no removals).
func Diff(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}

	var steps []Step

	// DNS: domains removed or service lost its domains.
	for svcName, domains := range prev.Domains {
		if _, ok := current.Domains[svcName]; !ok {
			steps = append(steps, Step{Kind: StepDNSDelete, Name: svcName, Params: map[string]any{
				"domains": []string(domains),
			}})
		}
	}

	// Services removed.
	for _, name := range sortedKeys(prev.Services) {
		if _, ok := current.Services[name]; !ok {
			steps = append(steps, Step{Kind: StepServiceDelete, Name: name})
		}
	}

	// Storage removed.
	for _, name := range sortedKeys(prev.Storage) {
		if _, ok := current.Storage[name]; !ok {
			steps = append(steps, Step{Kind: StepStorageDelete, Name: name})
		}
	}

	// Secrets: keys referenced in prev but not in current.
	prevSecrets := collectSecrets(prev)
	currentSecrets := map[string]bool{}
	for _, k := range collectSecrets(current) {
		currentSecrets[k] = true
	}
	for _, key := range prevSecrets {
		if !currentSecrets[key] {
			steps = append(steps, Step{Kind: StepSecretDelete, Name: key})
		}
	}

	// Volumes removed.
	for _, name := range sortedKeys(prev.Volumes) {
		if _, ok := current.Volumes[name]; !ok {
			steps = append(steps, Step{Kind: StepVolumeDelete, Name: name})
		}
	}

	// Compute: servers removed (workers first, master last).
	for _, name := range reverseSorted(removedKeys(prev.Servers, current.Servers)) {
		steps = append(steps, Step{Kind: StepComputeDelete, Name: name})
	}

	return steps
}

// FullPlan generates the complete deploy plan: delete removed resources first
// (reverse order), then set everything (forward order, idempotent).
//
// prev is the previous config version (nil for first deploy).
func FullPlan(prev, current *Config, env map[string]string) ([]Step, error) {
	setSteps, err := Plan(current, env)
	if err != nil {
		return nil, err
	}

	deleteSteps := Diff(prev, current)
	if len(deleteSteps) == 0 {
		return setSteps, nil
	}

	// Deletes first, then sets.
	return append(deleteSteps, setSteps...), nil
}

// removedKeys returns keys present in prev but absent in current.
func removedKeys[V any](prev, current map[string]V) []string {
	var removed []string
	for k := range prev {
		if _, ok := current[k]; !ok {
			removed = append(removed, k)
		}
	}
	return removed
}

// reverseSorted sorts strings and returns them in reverse order.
func reverseSorted(s []string) []string {
	sort.Sort(sort.Reverse(sort.StringSlice(s)))
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
