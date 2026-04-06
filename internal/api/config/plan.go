package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
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

// Plan generates the full ordered deploy sequence: deletes for removed resources
// first (reverse deploy order), then sets for desired resources (forward deploy order).
//
// prev is the previous config version (nil for first deploy).
// current is the desired state (empty for destroy-all).
// env is the parsed .env map — used to resolve secret values and env var references.
func Plan(prev, current *Config, env map[string]string) ([]Step, error) {
	var steps []Step

	// ── Deletes (reverse deploy order) ─────────────────────────────────────
	steps = append(steps, diffDNS(prev, current)...)
	steps = append(steps, diffServices(prev, current)...)
	steps = append(steps, diffStorage(prev, current)...)
	steps = append(steps, diffSecrets(prev, current)...)
	steps = append(steps, diffVolumes(prev, current)...)
	steps = append(steps, diffCompute(prev, current)...)

	// ── Sets (forward deploy order) ────────────────────────────────────────
	steps = append(steps, setCompute(current)...)
	steps = append(steps, setVolumes(current)...)
	steps = append(steps, setBuild(current)...)
	setSecrets, err := setSecrets(current, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, setSecrets...)
	steps = append(steps, setStorage(current)...)
	setServices, err := setServices(current, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, setServices...)
	steps = append(steps, setDNS(current)...)

	return steps, nil
}

// ── Set phases (forward deploy order) ──────────────────────────────────────

func setCompute(cfg *Config) []Step {
	var steps []Step
	serverNames := utils.SortedKeys(cfg.Servers)
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
	return steps
}

func setVolumes(cfg *Config) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Volumes) {
		vol := cfg.Volumes[name]
		steps = append(steps, Step{Kind: StepVolumeSet, Name: name, Params: map[string]any{
			"size":   vol.Size,
			"server": vol.Server,
		}})
	}
	return steps
}

func setBuild(cfg *Config) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Build) {
		b := cfg.Build[name]
		steps = append(steps, Step{Kind: StepBuild, Name: name, Params: map[string]any{
			"source": b.Source,
		}})
	}
	return steps
}

func setSecrets(cfg *Config, env map[string]string) ([]Step, error) {
	var steps []Step
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
	return steps, nil
}

func setStorage(cfg *Config) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Storage) {
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
	return steps
}

func setServices(cfg *Config, env map[string]string) ([]Step, error) {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Services) {
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
	return steps, nil
}

func setDNS(cfg *Config) []Step {
	var steps []Step
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		domains := cfg.Domains[svcName]
		steps = append(steps, Step{Kind: StepDNSSet, Name: svcName, Params: map[string]any{
			"domains": []string(domains),
		}})
	}
	return steps
}

// ── Diff phases (reverse deploy order) ─────────────────────────────────────

func diffDNS(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
	for svcName, domains := range prev.Domains {
		if _, ok := current.Domains[svcName]; !ok {
			steps = append(steps, Step{Kind: StepDNSDelete, Name: svcName, Params: map[string]any{
				"domains": []string(domains),
			}})
		}
	}
	return steps
}

func diffServices(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(prev.Services) {
		if _, ok := current.Services[name]; !ok {
			steps = append(steps, Step{Kind: StepServiceDelete, Name: name})
		}
	}
	return steps
}

func diffStorage(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(prev.Storage) {
		if _, ok := current.Storage[name]; !ok {
			steps = append(steps, Step{Kind: StepStorageDelete, Name: name})
		}
	}
	return steps
}

func diffSecrets(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
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
	return steps
}

func diffVolumes(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(prev.Volumes) {
		if _, ok := current.Volumes[name]; !ok {
			steps = append(steps, Step{Kind: StepVolumeDelete, Name: name})
		}
	}
	return steps
}

func diffCompute(prev, current *Config) []Step {
	if prev == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.ReverseSorted(utils.RemovedKeys(prev.Servers, current.Servers)) {
		steps = append(steps, Step{Kind: StepComputeDelete, Name: name})
	}
	return steps
}

// ── Helpers ────────────────────────────────────────────────────────────────

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
