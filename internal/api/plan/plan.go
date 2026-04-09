// Package plan generates ordered deployment step sequences from config diffs.
package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Cfg aliases config.Config for readability.
type Cfg = config.Config

// StepKind identifies the pkg/core/ function to call.
type StepKind string

const (
	StepComputeSet    StepKind = "instance.set"
	StepComputeDelete StepKind = "instance.delete"
	StepFirewallSet   StepKind = "firewall.set"
	StepVolumeSet     StepKind = "volume.set"
	StepVolumeDelete  StepKind = "volume.delete"
	StepBuild         StepKind = "build"
	StepSecretSet     StepKind = "secret.set"
	StepSecretDelete  StepKind = "secret.delete"
	StepStorageSet    StepKind = "storage.set"
	StepStorageDelete StepKind = "storage.delete"
	StepServiceSet    StepKind = "service.set"
	StepServiceDelete StepKind = "service.delete"
	StepCronSet       StepKind = "cron.set"
	StepCronDelete    StepKind = "cron.delete"
	StepDNSSet        StepKind = "dns.set"
	StepDNSDelete     StepKind = "dns.delete"
	StepIngressSet    StepKind = "ingress.set"
	StepIngressDelete StepKind = "ingress.delete"
)

// Step is one action in the deploy plan.
// Maps 1:1 to a pkg/core/ function call.
type Step struct {
	Kind   StepKind       `json:"kind"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

// BuildRequest holds all inputs for generating a deploy plan.
type BuildRequest struct {
	Reality *Cfg                // currently deployed config (nil for first deploy)
	Desired *Cfg                // target config
	Env     map[string]string   // parsed env — secrets and env var values
	Owned   map[string]bool     // resource names owned by managed compiler — Build skips them
	Exports map[string][]string // service name → injected secret keys from uses: references
}

// Build generates the full ordered deploy sequence.
func Build(req BuildRequest) ([]Step, error) {
	reality := req.Reality
	desired := req.Desired
	env := req.Env
	owned := req.Owned
	exports := req.Exports
	var steps []Step

	// ── Deletes (reverse deploy order) ─────────────────────────────────────
	ingressDiff, err := diffIngress(reality, desired, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, ingressDiff...)
	steps = append(steps, diffDNS(reality, desired)...)
	steps = append(steps, diffCrons(reality, desired, owned)...)
	steps = append(steps, diffServices(reality, desired, owned)...)
	steps = append(steps, diffStorage(reality, desired, owned)...)
	steps = append(steps, diffSecrets(reality, desired, owned, exports)...)
	steps = append(steps, diffVolumes(reality, desired, owned)...)
	steps = append(steps, diffFirewall(reality, desired)...)
	steps = append(steps, diffCompute(reality, desired)...)

	// ── Sets (forward deploy order) ────────────────────────────────────────
	steps = append(steps, setCompute(desired)...)
	steps = append(steps, setFirewall(desired)...)
	steps = append(steps, setVolumes(desired, owned)...)
	steps = append(steps, setBuild(desired)...)
	secretSteps, err := setSecrets(desired, env, owned, exports)
	if err != nil {
		return nil, err
	}
	steps = append(steps, secretSteps...)
	steps = append(steps, setStorage(desired, owned)...)
	serviceSteps, err := setServices(desired, env, owned, exports)
	if err != nil {
		return nil, err
	}
	steps = append(steps, serviceSteps...)
	cronSteps, err := setCrons(desired, env, owned)
	if err != nil {
		return nil, err
	}
	steps = append(steps, cronSteps...)
	steps = append(steps, setDNS(desired)...)
	ingressSteps, err := setIngress(desired, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, ingressSteps...)

	return steps, nil
}

// ── Set phases (forward deploy order) ──────────────────────────────────────

func setCompute(cfg *Cfg) []Step {
	// Master first, then workers — role is explicit.
	var masterSteps, workerSteps []Step
	for _, name := range utils.SortedKeys(cfg.Servers) {
		srv := cfg.Servers[name]
		params := map[string]any{"type": srv.Type, "region": srv.Region, "role": srv.Role}
		if srv.Role == "worker" {
			workerSteps = append(workerSteps, Step{Kind: StepComputeSet, Name: name, Params: params})
		} else {
			masterSteps = append(masterSteps, Step{Kind: StepComputeSet, Name: name, Params: params})
		}
	}
	return append(masterSteps, workerSteps...)
}

func setFirewall(cfg *Cfg) []Step {
	if cfg.Firewall == nil {
		return nil
	}
	params := map[string]any{}
	if cfg.Firewall.Preset != "" {
		params["preset"] = cfg.Firewall.Preset
	}
	if len(cfg.Firewall.Rules) > 0 {
		params["rules"] = cfg.Firewall.Rules
	}
	return []Step{{Kind: StepFirewallSet, Name: "firewall", Params: params}}
}

func setVolumes(cfg *Cfg, owned map[string]bool) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Volumes) {
		if owned[name] {
			continue
		}
		vol := cfg.Volumes[name]
		steps = append(steps, Step{Kind: StepVolumeSet, Name: name, Params: map[string]any{
			"size": vol.Size, "server": vol.Server,
		}})
	}
	return steps
}

func setBuild(cfg *Cfg) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Build) {
		steps = append(steps, Step{Kind: StepBuild, Name: name, Params: map[string]any{
			"source": cfg.Build[name].Source,
		}})
	}
	return steps
}

func setSecrets(cfg *Cfg, env map[string]string, owned map[string]bool, exports map[string][]string) ([]Step, error) {
	var steps []Step
	for _, key := range collectSecrets(cfg, exports) {
		if owned[key] {
			continue
		}
		val, ok := env[key]
		if !ok {
			return nil, fmt.Errorf("secret %q referenced by service but not found in env", key)
		}
		steps = append(steps, Step{Kind: StepSecretSet, Name: key, Params: map[string]any{"value": val}})
	}
	return steps, nil
}

func setStorage(cfg *Cfg, owned map[string]bool) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Storage) {
		if owned[name] {
			continue
		}
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

func setServices(cfg *Cfg, env map[string]string, owned map[string]bool, exports map[string][]string) ([]Step, error) {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Services) {
		if owned[name] {
			continue
		}
		svc := cfg.Services[name]
		params := map[string]any{}
		if _, isBuildTarget := cfg.Build[svc.Image]; isBuildTarget {
			params["build"] = svc.Image
		} else if svc.Image != "" {
			params["image"] = svc.Image
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
		secrets := svc.Secrets
		if extra, ok := exports[name]; ok {
			secrets = append(append([]string{}, secrets...), extra...)
		}
		if len(secrets) > 0 {
			params["secrets"] = secrets
		}
		if len(svc.Storage) > 0 {
			params["storage"] = svc.Storage
		}
		if len(svc.Env) > 0 {
			resolved, err := resolveEnvEntries(svc.Env, env, "services."+name)
			if err != nil {
				return nil, err
			}
			params["env"] = resolved
		}
		steps = append(steps, Step{Kind: StepServiceSet, Name: name, Params: params})
	}
	return steps, nil
}

func setCrons(cfg *Cfg, env map[string]string, owned map[string]bool) ([]Step, error) {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Crons) {
		if owned[name] {
			continue
		}
		cron := cfg.Crons[name]
		params := map[string]any{
			"schedule": cron.Schedule,
		}
		if _, isBuildTarget := cfg.Build[cron.Image]; isBuildTarget {
			params["build"] = cron.Image
		} else {
			params["image"] = cron.Image
		}
		if cron.Command != "" {
			params["command"] = cron.Command
		}
		if cron.Server != "" {
			params["server"] = cron.Server
		}
		if len(cron.Secrets) > 0 {
			params["secrets"] = cron.Secrets
		}
		if len(cron.Storage) > 0 {
			params["storage"] = cron.Storage
		}
		if len(cron.Volumes) > 0 {
			params["volumes"] = cron.Volumes
		}
		if len(cron.Env) > 0 {
			resolved, err := resolveEnvEntries(cron.Env, env, "crons."+name)
			if err != nil {
				return nil, err
			}
			params["env"] = resolved
		}
		steps = append(steps, Step{Kind: StepCronSet, Name: name, Params: params})
	}
	return steps, nil
}

func setDNS(cfg *Cfg) []Step {
	var steps []Step
	cloudflareManaged := cfg.Ingress != nil && cfg.Ingress.CloudflareManaged
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		params := map[string]any{
			"domains": []string(cfg.Domains[svcName]),
		}
		if cloudflareManaged {
			params["cloudflare_managed"] = true
		}
		steps = append(steps, Step{Kind: StepDNSSet, Name: svcName, Params: params})
	}
	return steps
}

func setIngress(cfg *Cfg, env map[string]string) ([]Step, error) {
	if len(cfg.Domains) == 0 {
		return nil, nil
	}
	var steps []Step
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		params, err := ingressRouteParams(cfg, svcName, env)
		if err != nil {
			return nil, err
		}
		steps = append(steps, Step{Kind: StepIngressSet, Name: svcName, Params: params})
	}
	return steps, nil
}

func ingressRouteParams(cfg *Cfg, svcName string, env map[string]string) (map[string]any, error) {
	params := map[string]any{
		"service": svcName,
		"domains": []string(cfg.Domains[svcName]),
	}
	if cfg.Ingress != nil && cfg.Ingress.CloudflareManaged {
		params["cloudflare_managed"] = true
	}
	if cfg.Ingress != nil && cfg.Ingress.Cert != "" {
		certPEM, ok := env[cfg.Ingress.Cert]
		if !ok {
			return nil, fmt.Errorf("ingress.cert: %q not found in env", cfg.Ingress.Cert)
		}
		keyPEM, ok := env[cfg.Ingress.Key]
		if !ok {
			return nil, fmt.Errorf("ingress.key: %q not found in env", cfg.Ingress.Key)
		}
		params["cert_pem"] = certPEM
		params["key_pem"] = keyPEM
	}
	return params, nil
}

// ── Diff phases (reverse deploy order) ─────────────────────────────────────

func diffIngress(reality, desired *Cfg, env map[string]string) ([]Step, error) {
	if reality == nil {
		return nil, nil
	}

	var steps []Step

	// Delete steps for removed domains.
	for _, svcName := range utils.SortedKeys(reality.Domains) {
		if _, ok := desired.Domains[svcName]; !ok {
			steps = append(steps, Step{Kind: StepIngressDelete, Name: svcName, Params: map[string]any{
				"service": svcName,
				"domains": []string(reality.Domains[svcName]),
			}})
		}
	}

	// Set steps for changed or new domains.
	if domainsChanged(reality, desired) {
		for _, svcName := range utils.SortedKeys(desired.Domains) {
			if _, ok := desired.Services[svcName]; !ok {
				continue
			}
			params, err := ingressRouteParams(desired, svcName, env)
			if err != nil {
				return nil, err
			}
			steps = append(steps, Step{Kind: StepIngressSet, Name: svcName, Params: params})
		}
	}

	return steps, nil
}

func diffDNS(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for svcName, domains := range reality.Domains {
		nextDomains, ok := desired.Domains[svcName]
		if !ok {
			steps = append(steps, Step{Kind: StepDNSDelete, Name: svcName, Params: map[string]any{
				"domains": []string(domains),
			}})
			continue
		}

		removed := removedDomains(domains, nextDomains)
		if len(removed) > 0 {
			steps = append(steps, Step{Kind: StepDNSDelete, Name: svcName, Params: map[string]any{
				"domains": removed,
			}})
		}
	}
	return steps
}

func diffServices(reality, desired *Cfg, owned map[string]bool) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Services) {
		if owned[name] {
			continue
		}
		if _, ok := desired.Services[name]; !ok {
			steps = append(steps, Step{Kind: StepServiceDelete, Name: name})
		}
	}
	return steps
}

func diffCrons(reality, desired *Cfg, owned map[string]bool) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Crons) {
		if owned[name] {
			continue
		}
		if _, ok := desired.Crons[name]; !ok {
			steps = append(steps, Step{Kind: StepCronDelete, Name: name})
		}
	}
	return steps
}

func diffStorage(reality, desired *Cfg, owned map[string]bool) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Storage) {
		if owned[name] {
			continue
		}
		if _, ok := desired.Storage[name]; !ok {
			steps = append(steps, Step{Kind: StepStorageDelete, Name: name})
		}
	}
	return steps
}

func diffSecrets(reality, desired *Cfg, owned map[string]bool, exports map[string][]string) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	realitySecrets := collectSecrets(reality, nil)
	desiredSecrets := map[string]bool{}
	for _, k := range collectSecrets(desired, exports) {
		desiredSecrets[k] = true
	}
	for _, key := range realitySecrets {
		if owned[key] {
			continue
		}
		if !desiredSecrets[key] {
			steps = append(steps, Step{Kind: StepSecretDelete, Name: key})
		}
	}
	return steps
}

func diffVolumes(reality, desired *Cfg, owned map[string]bool) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Volumes) {
		if owned[name] {
			continue
		}
		if _, ok := desired.Volumes[name]; !ok {
			steps = append(steps, Step{Kind: StepVolumeDelete, Name: name})
		}
	}
	return steps
}

func diffFirewall(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	// If previous config had firewall rules but new config doesn't,
	// reset to base rules (nil PortAllowList = SSH + internal only).
	if reality.Firewall != nil && desired.Firewall == nil {
		return []Step{{Kind: StepFirewallSet, Name: "firewall", Params: map[string]any{}}}
	}
	return nil
}

func diffCompute(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.ReverseSorted(utils.RemovedKeys(reality.Servers, desired.Servers)) {
		steps = append(steps, Step{Kind: StepComputeDelete, Name: name})
	}
	return steps
}

// ── Helpers ────────────────────────────────────────────────────────────────

func resolveEnvEntries(entries []string, env map[string]string, context string) ([]string, error) {
	var resolved []string
	for _, entry := range entries {
		if strings.Contains(entry, "=") {
			resolved = append(resolved, entry)
		} else {
			val, ok := env[entry]
			if !ok {
				return nil, fmt.Errorf("%s.env: %q not found in env", context, entry)
			}
			resolved = append(resolved, entry+"="+val)
		}
	}
	return resolved, nil
}

func collectSecrets(cfg *Cfg, exports map[string][]string) []string {
	seen := map[string]bool{}
	for _, svc := range cfg.Services {
		for _, entry := range svc.Secrets {
			seen[secretKey(entry)] = true
		}
	}
	for _, cron := range cfg.Crons {
		for _, entry := range cron.Secrets {
			seen[secretKey(entry)] = true
		}
	}
	for _, keys := range exports {
		for _, k := range keys {
			seen[secretKey(k)] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func secretKey(entry string) string {
	if _, key, ok := strings.Cut(entry, "="); ok {
		return key
	}
	return entry
}

func domainsChanged(reality, desired *Cfg) bool {
	if len(reality.Domains) != len(desired.Domains) {
		return true
	}
	for svc, domains := range reality.Domains {
		other, ok := desired.Domains[svc]
		if !ok {
			return true
		}
		if len(domains) != len(other) {
			return true
		}
		for i := range domains {
			if domains[i] != other[i] {
				return true
			}
		}
	}
	// Ingress config changed (e.g. switched to/from cloudflare-managed).
	if ingressConfigChanged(reality.Ingress, desired.Ingress) {
		return true
	}
	return false
}

func ingressConfigChanged(a, b *config.IngressConfig) bool {
	if (a == nil) != (b == nil) {
		return true
	}
	if a == nil {
		return false
	}
	return a.CloudflareManaged != b.CloudflareManaged || a.Cert != b.Cert || a.Key != b.Key
}

func removedDomains(prev, next []string) []string {
	keep := map[string]bool{}
	for _, domain := range next {
		keep[domain] = true
	}

	var removed []string
	for _, domain := range prev {
		if !keep[domain] {
			removed = append(removed, domain)
		}
	}
	return removed
}
