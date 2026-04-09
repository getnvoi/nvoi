package plan

import (
	"fmt"
	"sort"

	"github.com/getnvoi/nvoi/internal/api/config"
	"github.com/getnvoi/nvoi/pkg/managed"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// ResolvedSteps is the output of ResolveDeploymentSteps: the full ordered step
// sequence plus managed secret values needed by the caller.
type ResolvedSteps struct {
	Steps   []Step            // final ordered deployment steps
	Config  *config.Config    // stripped config (managed-owned resources removed) for validation
	Secrets map[string]string // managed secrets to merge into env
}

// ResolveDeploymentSteps compiles managed bundles, strips managed-owned resources
// from the config, calls Build() for non-managed resources, and merges the results
// into a single ordered step sequence.
//
// Managed credentials are required in env. Missing credentials = hard error from
// the managed compiler. No credential generation, no credential persistence.
//
// This is the API step resolution layer. The local CLI does not use this — it
// compiles bundles via pkg/managed and executes operations directly.
func ResolveDeploymentSteps(cfg, current *config.Config, env map[string]string) (*ResolvedSteps, error) {
	defaultServer := firstServer(cfg)

	var managedSetSteps []Step
	var managedDeleteSteps []Step
	flatSecrets := map[string]string{}
	owned := map[string]bool{} // all resource names the compiler owns — Build() skips these
	exportedKeys := map[string][]string{}
	managedNames := map[string]bool{}

	// Compile each managed service into a bundle and collect steps.
	for _, name := range utils.SortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if svc.Managed == "" {
			continue
		}
		managedNames[name] = true

		result, err := managed.Compile(managed.Request{
			Kind: svc.Managed,
			Name: name,
			Env:  env,
			Params: map[string]any{
				"image":          svc.Image,
				"volume_size":    svc.VolumeSize,
				"backup_storage": svc.BackupStorage,
				"backup_cron":    svc.BackupCron,
			},
			Context: managed.Context{DefaultVolumeServer: defaultServer},
		})
		if err != nil {
			return nil, fmt.Errorf("services.%s.managed: %w", name, err)
		}

		// Collect secrets + build owned set.
		for key, value := range result.Bundle.InternalSecrets {
			flatSecrets[key] = value
			owned[key] = true
		}
		for key, value := range result.Bundle.ExportedSecrets {
			flatSecrets[key] = value
			owned[key] = true
		}
		owned[name] = true // the service itself
		for _, vol := range result.Bundle.Volumes {
			owned[vol.Name] = true
		}
		for _, st := range result.Bundle.Storages {
			owned[st.Name] = true
		}
		for _, cr := range result.Bundle.Crons {
			owned[cr.Name] = true
		}

		// Track exported keys for uses: injection.
		keys := sortedMapKeys(result.Bundle.ExportedSecrets)
		exportedKeys[name] = keys

		managedSetSteps = append(managedSetSteps, bundleToSetSteps(result.Bundle)...)
	}

	// Build exports map: service name → injected secret keys from uses: references.
	// Passed to Build() as an explicit input — no config mutation.
	exports := map[string][]string{}
	for _, name := range utils.SortedKeys(cfg.Services) {
		svc := cfg.Services[name]
		if len(svc.Uses) == 0 {
			continue
		}
		for _, ref := range svc.Uses {
			keys, ok := exportedKeys[ref]
			if !ok {
				return nil, fmt.Errorf("services.%s.uses: %q has no managed exports", name, ref)
			}
			exports[name] = append(exports[name], keys...)
		}
	}

	// Generate delete steps for managed services that exist in current (reality)
	// but are absent from the desired config. Uses Shape — no credential values needed.
	if current != nil {
		for _, name := range utils.SortedKeys(current.Services) {
			svc := current.Services[name]
			if svc.Managed == "" {
				continue
			}
			if managedNames[name] {
				continue // still desired
			}
			shape, err := managed.Shape(svc.Managed, name)
			if err != nil {
				managedDeleteSteps = append(managedDeleteSteps, Step{Kind: StepServiceDelete, Name: name})
				continue
			}
			for _, key := range shape.SecretKeys {
				owned[key] = true
			}
			for _, v := range shape.Volumes {
				owned[v] = true
			}
			for _, s := range shape.Storages {
				owned[s] = true
			}
			for _, c := range shape.Crons {
				owned[c] = true
			}
			for _, s := range shape.Services {
				owned[s] = true
			}
			managedDeleteSteps = append(managedDeleteSteps, shapeToDeleteSteps(shape)...)
		}
	}

	// Merge managed secrets into env for Build() validation.
	mergedEnv := make(map[string]string, len(env)+len(flatSecrets))
	for k, v := range env {
		mergedEnv[k] = v
	}
	for k, v := range flatSecrets {
		mergedEnv[k] = v
	}

	// Build non-managed steps — owned set tells Build() what to skip,
	// exports tells it which secrets to inject into consuming services.
	nonManagedSteps, err := Build(BuildRequest{
		Reality: current,
		Desired: cfg,
		Env:     mergedEnv,
		Owned:   owned,
		Exports: exports,
	})
	if err != nil {
		return nil, err
	}

	// Merge all steps in deployment phase order.
	steps := mergeSteps(nonManagedSteps, managedDeleteSteps, managedSetSteps)

	return &ResolvedSteps{
		Steps:   steps,
		Config:  cfg,
		Secrets: flatSecrets,
	}, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

func firstServer(cfg *config.Config) string {
	for _, name := range utils.SortedKeys(cfg.Servers) {
		return name
	}
	return ""
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func bundleToSetSteps(bundle managed.Bundle) []Step {
	steps := make([]Step, 0, len(bundle.Operations))
	for _, op := range bundle.Operations {
		steps = append(steps, Step{
			Kind:   StepKind(op.Kind),
			Name:   op.Name,
			Params: op.Params,
		})
	}
	return steps
}

func shapeToDeleteSteps(shape managed.BundleShape) []Step {
	var steps []Step
	for _, name := range shape.Crons {
		steps = append(steps, Step{Kind: StepCronDelete, Name: name})
	}
	for _, name := range shape.Services {
		steps = append(steps, Step{Kind: StepServiceDelete, Name: name})
	}
	for _, name := range shape.Storages {
		steps = append(steps, Step{Kind: StepStorageDelete, Name: name})
	}
	for _, key := range shape.SecretKeys {
		steps = append(steps, Step{Kind: StepSecretDelete, Name: key})
	}
	for _, name := range shape.Volumes {
		steps = append(steps, Step{Kind: StepVolumeDelete, Name: name})
	}
	return steps
}

// mergeSteps interleaves managed steps into the non-managed plan output,
// respecting deployment phase ordering: deletes before sets, managed deletes
// with other deletes, managed sets with other sets.
func mergeSteps(nonManaged, managedDeletes, managedSets []Step) []Step {
	// Find the boundary between delete phase and set phase in non-managed steps.
	setStart := len(nonManaged)
	for i, step := range nonManaged {
		if isSetKind(step.Kind) {
			setStart = i
			break
		}
	}

	deletePhase := nonManaged[:setStart]
	setPhase := nonManaged[setStart:]

	// Insert managed deletes after ingress/dns deletes but before other deletes.
	deleteAnchor := len(deletePhase)
	for i, step := range deletePhase {
		if step.Kind != StepIngressDelete && step.Kind != StepIngressSet && step.Kind != StepDNSDelete {
			deleteAnchor = i
			break
		}
	}

	// Insert managed sets before the first non-infra set step.
	setAnchor := len(setPhase)
	for i, step := range setPhase {
		if step.Kind == StepSecretSet || step.Kind == StepStorageSet || step.Kind == StepServiceSet || step.Kind == StepDNSSet || step.Kind == StepIngressSet {
			setAnchor = i
			break
		}
	}

	var steps []Step
	steps = append(steps, deletePhase[:deleteAnchor]...)
	steps = append(steps, managedDeletes...)
	steps = append(steps, deletePhase[deleteAnchor:]...)
	steps = append(steps, setPhase[:setAnchor]...)
	steps = append(steps, orderManagedSets(managedSets)...)
	steps = append(steps, setPhase[setAnchor:]...)
	return steps
}

func isSetKind(kind StepKind) bool {
	switch kind {
	case StepComputeSet, StepFirewallSet, StepVolumeSet, StepBuild,
		StepSecretSet, StepStorageSet, StepServiceSet, StepCronSet, StepDNSSet, StepIngressSet:
		return true
	}
	return false
}

// orderManagedSets sorts managed set steps in deterministic deployment order:
// secrets -> storage -> volumes -> services -> crons.
func orderManagedSets(steps []Step) []Step {
	var secrets, storages, volumes, services, crons, other []Step
	for _, step := range steps {
		switch step.Kind {
		case StepSecretSet:
			secrets = append(secrets, step)
		case StepStorageSet:
			storages = append(storages, step)
		case StepVolumeSet:
			volumes = append(volumes, step)
		case StepServiceSet:
			services = append(services, step)
		case StepCronSet:
			crons = append(crons, step)
		default:
			other = append(other, step)
		}
	}

	var ordered []Step
	ordered = append(ordered, secrets...)
	ordered = append(ordered, storages...)
	ordered = append(ordered, volumes...)
	ordered = append(ordered, services...)
	ordered = append(ordered, crons...)
	ordered = append(ordered, other...)
	return ordered
}
