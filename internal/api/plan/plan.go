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
	StepDNSSet        StepKind = "dns.set"
	StepDNSDelete     StepKind = "dns.delete"
	StepIngressApply  StepKind = "ingress.apply"
)

// Step is one action in the deploy plan.
// Maps 1:1 to a pkg/core/ function call.
type Step struct {
	Kind   StepKind       `json:"kind"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params,omitempty"`
}

const (
	ingressExposureDirect      = "direct"
	ingressExposureEdgeProxied = "edge_proxied"

	ingressTLSACME       = "acme"
	ingressTLSProvided   = "provided"
	ingressTLSEdgeOrigin = "edge_origin"
)

// Plan generates the full ordered deploy sequence: deletes for removed resources
// first (reverse deploy order), then sets for desired resources (forward deploy order).
//
// reality is what's currently deployed (from InfraState, nil for first deploy).
// desired is the target state (empty for destroy-all).
// env is the parsed .env map — used to resolve secret values and env var references.
func Build(reality, desired *Cfg, env map[string]string) ([]Step, error) {
	var steps []Step

	// ── Deletes (reverse deploy order) ─────────────────────────────────────
	ingressDiff, err := diffIngress(reality, desired, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, ingressDiff...)
	steps = append(steps, diffDNS(reality, desired)...)
	steps = append(steps, diffServices(reality, desired)...)
	steps = append(steps, diffStorage(reality, desired)...)
	steps = append(steps, diffSecrets(reality, desired)...)
	steps = append(steps, diffVolumes(reality, desired)...)
	steps = append(steps, diffFirewall(reality, desired)...)
	steps = append(steps, diffCompute(reality, desired)...)

	// ── Sets (forward deploy order) ────────────────────────────────────────
	steps = append(steps, setCompute(desired)...)
	steps = append(steps, setFirewall(desired)...)
	steps = append(steps, setVolumes(desired)...)
	steps = append(steps, setBuild(desired)...)
	secretSteps, err := setSecrets(desired, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, secretSteps...)
	steps = append(steps, setStorage(desired)...)
	serviceSteps, err := setServices(desired, env)
	if err != nil {
		return nil, err
	}
	steps = append(steps, serviceSteps...)
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
	var steps []Step
	for i, name := range utils.SortedKeys(cfg.Servers) {
		srv := cfg.Servers[name]
		params := map[string]any{"type": srv.Type, "region": srv.Region}
		if i > 0 {
			params["worker"] = true
		}
		steps = append(steps, Step{Kind: StepComputeSet, Name: name, Params: params})
	}
	return steps
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

func setVolumes(cfg *Cfg) []Step {
	var steps []Step
	for _, name := range utils.SortedKeys(cfg.Volumes) {
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

func setSecrets(cfg *Cfg, env map[string]string) ([]Step, error) {
	var steps []Step
	for _, key := range collectSecrets(cfg) {
		val, ok := env[key]
		if !ok {
			return nil, fmt.Errorf("secret %q referenced by service but not found in env", key)
		}
		steps = append(steps, Step{Kind: StepSecretSet, Name: key, Params: map[string]any{"value": val}})
	}
	return steps, nil
}

func setStorage(cfg *Cfg) []Step {
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

func setServices(cfg *Cfg, env map[string]string) ([]Step, error) {
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

func setDNS(cfg *Cfg) []Step {
	var steps []Step
	edgeProxied := desiredIngressExposure(cfg) == ingressExposureEdgeProxied
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		params := map[string]any{
			"domains": []string(cfg.Domains[svcName]),
		}
		if edgeProxied {
			params["edge_proxied"] = true
		}
		steps = append(steps, Step{Kind: StepDNSSet, Name: svcName, Params: params})
	}
	return steps
}

func setIngress(cfg *Cfg, env map[string]string) ([]Step, error) {
	if len(cfg.Domains) == 0 {
		return nil, nil
	}
	routes := ingressRoutes(cfg, nil)
	params, err := ingressParams(cfg, routes, env)
	if err != nil {
		return nil, err
	}
	return []Step{{Kind: StepIngressApply, Name: "ingress", Params: params}}, nil
}

func ingressRoutes(cfg *Cfg, allowedServices map[string]bool) []map[string]any {
	var routes []map[string]any
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		if allowedServices != nil && !allowedServices[svcName] {
			continue
		}
		routes = append(routes, map[string]any{
			"service": svcName,
			"domains": []string(cfg.Domains[svcName]),
		})
	}
	return routes
}

// ── Diff phases (reverse deploy order) ─────────────────────────────────────

func diffIngress(reality, desired *Cfg, env map[string]string) ([]Step, error) {
	if reality == nil || !domainsChanged(reality, desired) {
		return nil, nil
	}

	allowed := map[string]bool{}
	for name := range reality.Services {
		if _, ok := desired.Services[name]; ok {
			allowed[name] = true
		}
	}
	routes := ingressRoutes(desired, allowed)
	params, err := ingressParams(desired, routes, env)
	if err != nil {
		return nil, err
	}
	return []Step{{Kind: StepIngressApply, Name: "ingress", Params: params}}, nil
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

func diffServices(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Services) {
		if _, ok := desired.Services[name]; !ok {
			steps = append(steps, Step{Kind: StepServiceDelete, Name: name})
		}
	}
	return steps
}

func diffStorage(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Storage) {
		if _, ok := desired.Storage[name]; !ok {
			steps = append(steps, Step{Kind: StepStorageDelete, Name: name})
		}
	}
	return steps
}

func diffSecrets(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	realitySecrets := collectSecrets(reality)
	desiredSecrets := map[string]bool{}
	for _, k := range collectSecrets(desired) {
		desiredSecrets[k] = true
	}
	for _, key := range realitySecrets {
		if !desiredSecrets[key] {
			steps = append(steps, Step{Kind: StepSecretDelete, Name: key})
		}
	}
	return steps
}

func diffVolumes(reality, desired *Cfg) []Step {
	if reality == nil {
		return nil
	}
	var steps []Step
	for _, name := range utils.SortedKeys(reality.Volumes) {
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

func collectSecrets(cfg *Cfg) []string {
	seen := map[string]bool{}
	for _, svc := range cfg.Services {
		for _, entry := range svc.Secrets {
			seen[secretKey(entry)] = true
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
		if desiredIngressExposure(reality) != desiredIngressExposure(desired) {
			return true
		}
	}
	if desiredIngressTLSMode(reality) != desiredIngressTLSMode(desired) {
		return true
	}
	return false
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

func desiredIngressExposure(cfg *Cfg) string {
	if cfg == nil {
		return ingressExposureDirect
	}
	if cfg.Ingress != nil && cfg.Ingress.Exposure != "" {
		return cfg.Ingress.Exposure
	}
	return ingressExposureDirect
}

func desiredIngressTLSMode(cfg *Cfg) string {
	if cfg == nil {
		return ingressTLSACME
	}
	if cfg.Ingress != nil && cfg.Ingress.TLS != nil && cfg.Ingress.TLS.Mode != "" {
		return cfg.Ingress.TLS.Mode
	}
	if desiredIngressExposure(cfg) == ingressExposureEdgeProxied {
		return ingressTLSEdgeOrigin
	}
	return ingressTLSACME
}

func ingressParams(cfg *Cfg, routes []map[string]any, env map[string]string) (map[string]any, error) {
	params := map[string]any{
		"routes":   routes,
		"tls_mode": desiredIngressTLSMode(cfg),
		"exposure": desiredIngressExposure(cfg),
	}

	if cfg != nil && cfg.Ingress != nil && cfg.Ingress.Edge != nil && cfg.Ingress.Edge.Provider != "" {
		params["edge_provider"] = cfg.Ingress.Edge.Provider
	}

	if cfg != nil && cfg.Ingress != nil && cfg.Ingress.TLS != nil && desiredIngressTLSMode(cfg) == ingressTLSProvided {
		certRef := cfg.Ingress.TLS.Cert
		keyRef := cfg.Ingress.TLS.Key
		certPEM, ok := env[certRef]
		if !ok {
			return nil, fmt.Errorf("ingress.tls.cert: %q not found in env", certRef)
		}
		keyPEM, ok := env[keyRef]
		if !ok {
			return nil, fmt.Errorf("ingress.tls.key: %q not found in env", keyRef)
		}
		params["cert_pem"] = certPEM
		params["key_pem"] = keyPEM
	}

	return params, nil
}
