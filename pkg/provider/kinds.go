package provider

import (
	"fmt"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// Layer is the architectural axis CLAUDE.md's "two-layer core" model
// describes — provider infra vs k8s manifests. Every Kind belongs to
// exactly one layer; reconcile.Deploy uses this to choose between
// Bootstrap (loud per-resource output) and Connect (read-only attach)
// when no infra-side change exists in the plan.
type Layer string

const (
	// LayerInfra: provider-side resources (Hetzner / AWS / Scaleway
	// API objects, DNS records, object-storage buckets, SaaS database
	// instances, provider-side tunnel objects).
	LayerInfra Layer = "infra"
	// LayerCluster: k8s-side resources (apiserver writes — Deployments,
	// StatefulSets, Services, Secrets, ConfigMaps, CronJobs, PVCs).
	LayerCluster Layer = "cluster"
)

// Kind is nvoi's reconcile-level resource taxonomy — operator-facing,
// stable, the value plan output / describe / resources columns use.
// Distinct from kube.Kind (apiserver wire protocol): one nvoi.Kind
// often maps to multiple kube.Kinds (a service workload reconciles
// Deployment OR StatefulSet plus Service plus Secret), and one
// kube.Kind is shared across many nvoi.Kinds (Secrets are owned by
// services, crons, databases, registries, tunnel agents).
//
// The kindRegistry table below is the single source of truth — every
// Kind has exactly one row declaring its Layer, owner label (for
// cluster-side kinds), and the apiserver kinds the reconcile path
// touches. init() asserts completeness on package load.
type Kind string

const (
	// ── Infra layer ────────────────────────────────────────────────
	KindServer       Kind = "server"
	KindFirewall     Kind = "firewall"
	KindFirewallRule Kind = "firewall-rule"
	KindVolume       Kind = "volume"
	KindNetwork      Kind = "network"
	KindDNSRecord    Kind = "dns"
	KindBucket       Kind = "bucket"
	KindDatabase     Kind = "database" // straddles in spirit but the provider-side resource (Neon branch / selfhosted PG StatefulSet) is the lifecycle anchor
	KindTunnel       Kind = "tunnel"   // provider-side tunnel object (CF tunnel id / ngrok reserved domain)

	// ── Cluster layer ──────────────────────────────────────────────
	KindNamespace       Kind = "namespace"
	KindRegistrySecret  Kind = "registry-secret"
	KindServiceWorkload Kind = "service"      // user service: Deployment|StatefulSet + Service + per-svc Secret
	KindCronWorkload    Kind = "cronjob"      // user cron: CronJob + per-cron Secret
	KindSecretKey       Kind = "secret-key"   // sub-resource: a single key inside a per-workload Secret
	KindCaddyIngress    Kind = "caddy"        // bootstrap workloads: Deployment + Service + ConfigMap + PVC in kube-system
	KindTunnelAgent     Kind = "tunnel-agent" // cluster-side agent for the LayerInfra KindTunnel
	// KindDatabaseBranch: ephemeral per-branch sibling workloads
	// (PVC + Service + StatefulSet) created by `nvoi database branch`.
	// Distinct owner from KindDatabase so the parent-DB orphan sweep
	// in reconcile.Databases doesn't eat live branches.
	KindDatabaseBranch Kind = "database-branch"
)

// kindMeta is the projection table — one row per Kind. Adding a Kind
// = adding the constant above + one row here. init() walks both lists
// to guarantee they stay in lockstep.
type kindMeta struct {
	layer      Layer
	ownerLabel string      // value stamped on LabelNvoiOwner; "" for infra-side + namespace
	kubeKinds  []kube.Kind // apiserver kinds reconcile sweeps for this Kind; nil for infra-side + namespace
}

var kindRegistry = map[Kind]kindMeta{
	// Infra layer — no k8s footprint, no owner label, no kube kinds.
	KindServer:       {layer: LayerInfra},
	KindFirewall:     {layer: LayerInfra},
	KindFirewallRule: {layer: LayerInfra},
	KindVolume:       {layer: LayerInfra},
	KindNetwork:      {layer: LayerInfra},
	KindDNSRecord:    {layer: LayerInfra},
	KindBucket:       {layer: LayerInfra},
	KindTunnel:       {layer: LayerInfra},

	// Database is special: provider-side lifecycle (Neon API / PG
	// StatefulSet via CSI) AND k8s side (credentials Secret + backup
	// CronJob + backup-creds Secret + StatefulSet + Service + PVC for
	// selfhosted). Layer=Cluster because the SWEEP scope (orphan
	// detection on cfg removal) is k8s-side; the provider-side
	// lifecycle goes through DatabaseProvider.Delete which doesn't
	// use the owner-label machinery.
	KindDatabase: {
		layer:      LayerCluster,
		ownerLabel: utils.OwnerDatabases,
		kubeKinds: []kube.Kind{
			kube.KindStatefulSet, kube.KindService, kube.KindPVC,
			kube.KindCronJob, kube.KindSecret,
		},
	},

	// Cluster layer. ownerLabel values reference utils.Owner* — that
	// package is the single string source of truth (pkg/kube uses it
	// directly for its own caddy bootstrap; reconcile/core go through
	// Kind.OwnerLabel()). Editing the string here = editing it there.
	KindNamespace:      {layer: LayerCluster}, // EnsureNamespace path; not owner-labeled
	KindRegistrySecret: {layer: LayerCluster, ownerLabel: utils.OwnerRegistries, kubeKinds: []kube.Kind{kube.KindSecret}},
	KindServiceWorkload: {
		layer: LayerCluster, ownerLabel: utils.OwnerServices,
		kubeKinds: []kube.Kind{kube.KindDeployment, kube.KindStatefulSet, kube.KindService, kube.KindSecret},
	},
	KindCronWorkload: {
		layer: LayerCluster, ownerLabel: utils.OwnerCrons,
		kubeKinds: []kube.Kind{kube.KindCronJob, kube.KindSecret},
	},
	// KindSecretKey is plan-output-only — sub-resource of the per-
	// workload Secret. Inherits parent's owner label so any helper
	// that asks "which owner label?" still works. KubeKinds is nil
	// because there's no apiserver-level "secret-key" — sweep ops
	// scope to the parent kind (KindServiceWorkload's Secret entry).
	KindSecretKey: {layer: LayerCluster, ownerLabel: utils.OwnerServices},
	KindCaddyIngress: {
		layer: LayerCluster, ownerLabel: utils.OwnerCaddy,
		kubeKinds: []kube.Kind{kube.KindDeployment, kube.KindService, kube.KindConfigMap, kube.KindPVC},
	},
	KindTunnelAgent: {
		layer: LayerCluster, ownerLabel: utils.OwnerTunnel,
		kubeKinds: []kube.Kind{kube.KindDeployment, kube.KindSecret, kube.KindConfigMap},
	},
	KindDatabaseBranch: {
		layer: LayerCluster, ownerLabel: utils.OwnerDatabaseBranches,
		kubeKinds: []kube.Kind{kube.KindStatefulSet, kube.KindService, kube.KindPVC},
	},
}

// allKinds enumerates every Kind constant — used by init() to verify
// the registry is exhaustive. Adding a Kind requires adding it both
// to the const block AND to this slice; init() then verifies the
// kindRegistry has a row for it.
var allKinds = []Kind{
	KindServer, KindFirewall, KindFirewallRule, KindVolume, KindNetwork,
	KindDNSRecord, KindBucket, KindDatabase, KindTunnel,
	KindNamespace, KindRegistrySecret, KindServiceWorkload, KindCronWorkload,
	KindSecretKey, KindCaddyIngress, KindTunnelAgent, KindDatabaseBranch,
}

// ownerToKind is the reverse projection — built once at init() from
// the kindRegistry. Multiple Kinds can share the same owner label
// (KindServiceWorkload + KindSecretKey both use "services"); the
// table maps to the PARENT kind (the one with KubeKinds populated)
// since reverse lookup is used by the resources/describe path which
// always wants the apiserver-touching kind.
var ownerToKind map[string]Kind

func init() {
	// Completeness: every Kind constant must have a kindMeta row.
	for _, k := range allKinds {
		if _, ok := kindRegistry[k]; !ok {
			panic(fmt.Sprintf("provider.kindRegistry missing row for Kind %q — add it to the table in pkg/provider/kinds.go", k))
		}
	}
	// Symmetry: every kindRegistry row must correspond to a constant.
	known := map[Kind]bool{}
	for _, k := range allKinds {
		known[k] = true
	}
	for k := range kindRegistry {
		if !known[k] {
			panic(fmt.Sprintf("provider.kindRegistry has row for unknown Kind %q — add it to allKinds or remove the row", k))
		}
	}
	// Build the reverse owner-label index, preferring kinds with non-
	// nil KubeKinds (the parent kind) when multiple share an owner.
	ownerToKind = map[string]Kind{}
	for _, k := range allKinds {
		meta := kindRegistry[k]
		if meta.ownerLabel == "" {
			continue
		}
		// Only register if no kind is already mapped, OR the new kind
		// has KubeKinds and the existing one doesn't (parent-wins).
		existing, ok := ownerToKind[meta.ownerLabel]
		if !ok {
			ownerToKind[meta.ownerLabel] = k
			continue
		}
		if len(meta.kubeKinds) > 0 && len(kindRegistry[existing].kubeKinds) == 0 {
			ownerToKind[meta.ownerLabel] = k
		}
	}
}

// Layer returns the architectural layer this Kind belongs to.
func (k Kind) Layer() Layer { return kindRegistry[k].layer }

// OwnerLabel returns the value stamped on `nvoi/owner=` for k8s
// objects this Kind reconciles. Empty for infra-side kinds and the
// namespace (which doesn't carry an owner — it's the parent scope).
func (k Kind) OwnerLabel() string { return kindRegistry[k].ownerLabel }

// KubeKinds returns the apiserver kinds reconcile.SweepOwned /
// ListOwned must walk to find every k8s object this nvoi.Kind owns.
// nil for infra-side kinds + namespace + secret-key (sub-resource).
//
// Returned slice MUST NOT be mutated — it's the registry's slice,
// shared across all callers.
func (k Kind) KubeKinds() []kube.Kind { return kindRegistry[k].kubeKinds }

// IsInfra is shorthand for k.Layer() == LayerInfra. Used by Plan
// classification (drives Deploy's loud/quiet path branch).
func (k Kind) IsInfra() bool { return k.Layer() == LayerInfra }

// IsCluster is shorthand for k.Layer() == LayerCluster.
func (k Kind) IsCluster() bool { return k.Layer() == LayerCluster }

// KindFromOwnerLabel reverses OwnerLabel — given a label value
// stamped on a live k8s object, returns the parent nvoi.Kind. Used
// when classifying live resources by their nvoi/owner annotation.
// Returns ("", false) for unknown labels (external resources, pre-
// migration objects without LabelNvoiOwner).
func KindFromOwnerLabel(label string) (Kind, bool) {
	k, ok := ownerToKind[label]
	return k, ok
}

// AllKinds returns a copy of every registered Kind in declaration
// order. Used by tests + by classifiers that need to walk every kind.
func AllKinds() []Kind {
	out := make([]Kind, len(allKinds))
	copy(out, allKinds)
	return out
}
