package provider

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// Kind-aware wrappers around the low-level kube.Client owner-scoped
// operations. pkg/kube stays generic (knows about k8s GVR + the
// nvoi/owner label name); pkg/provider is the layer that knows which
// nvoi.Kind owns which kube.Kinds — so the wrappers live here.
//
// Callers that previously hand-rolled
//
//	for _, sweep := range []struct{ kind kube.Kind; desired []string }{
//	    {kube.KindDeployment, svcNames},
//	    {kube.KindStatefulSet, svcNames},
//	    {kube.KindService, svcNames},
//	    {kube.KindSecret, desiredSecrets},
//	} {
//	    kc.SweepOwned(ctx, ns, utils.OwnerServices, sweep.kind, sweep.desired)
//	}
//
// collapse to one call: SweepOwnedAll(ctx, kc, ns, KindServiceWorkload, svcNames).
// The Kind's KubeKinds table-row drives the iteration.

// ApplyOwned stamps the owner label derived from `kind` on `obj` and
// applies it via kc.ApplyOwned. Errors when `kind` has no owner label
// (infra-side or namespace) — those operations don't go through the
// owner-label machinery.
func ApplyOwned(ctx context.Context, kc *kube.Client, ns string, kind Kind, obj runtime.Object) error {
	owner := kind.OwnerLabel()
	if owner == "" {
		return fmt.Errorf("provider.ApplyOwned: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	return kc.ApplyOwned(ctx, ns, owner, obj)
}

// EnsureSecret stamps the owner label derived from `kind` and writes
// the per-workload Secret via kc.EnsureSecret. Same kind→owner
// derivation as ApplyOwned. Errors when `kind` has no owner label.
func EnsureSecret(ctx context.Context, kc *kube.Client, ns string, kind Kind, name string, kvs map[string]string) error {
	owner := kind.OwnerLabel()
	if owner == "" {
		return fmt.Errorf("provider.EnsureSecret: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	return kc.EnsureSecret(ctx, ns, owner, name, kvs)
}

// ListOwned returns names of `kubeKind` objects in `ns` carrying
// `kind`'s owner label. Thin wrapper over kc.ListOwned that drops the
// raw owner-string parameter — caller passes the typed Kind, kubeKind
// stays explicit because callers often want one specific apiserver
// kind (vs ListOwnedAll which walks every kube.Kind for the Kind).
func ListOwned(ctx context.Context, kc *kube.Client, ns string, kind Kind, kubeKind kube.Kind) ([]string, error) {
	owner := kind.OwnerLabel()
	if owner == "" {
		return nil, fmt.Errorf("provider.ListOwned: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	return kc.ListOwned(ctx, ns, owner, kubeKind)
}

// SweepOwned sweeps a single kube.Kind under `kind`'s owner label —
// for cases where the caller wants per-kube.Kind control (e.g. the
// reconcile.Crons step sweeps CronJob with cronNames but Secret with
// derived per-cron secret names). For the simpler all-kinds-share-the-
// same-desired-set case, use SweepOwnedAll.
func SweepOwned(ctx context.Context, kc *kube.Client, ns string, kind Kind, kubeKind kube.Kind, desired []string) error {
	owner := kind.OwnerLabel()
	if owner == "" {
		return fmt.Errorf("provider.SweepOwned: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	return kc.SweepOwned(ctx, ns, owner, kubeKind, desired)
}

// SweepOwnedAll sweeps every kube.Kind associated with `kind`,
// scoping each list by the kind's owner label. `desired` is the
// per-name keep-set — the same value is applied to every kube.Kind
// under this nvoi.Kind, which matches today's hand-rolled loops where
// e.g. a service's Deployment + StatefulSet + Service all share the
// same name.
//
// For nvoi.Kinds whose constituent kube.Kinds carry DIFFERENT desired
// sets (e.g. KindServiceWorkload's Secrets are <name>-secrets, not
// just <name>), pass perKubeKind explicitly via SweepOwnedFor.
func SweepOwnedAll(ctx context.Context, kc *kube.Client, ns string, kind Kind, desired []string) error {
	owner := kind.OwnerLabel()
	if owner == "" {
		return fmt.Errorf("provider.SweepOwnedAll: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	for _, kk := range kind.KubeKinds() {
		if err := kc.SweepOwned(ctx, ns, owner, kk, desired); err != nil {
			return err
		}
	}
	return nil
}

// SweepOwnedFor is the explicit form of SweepOwnedAll: caller supplies
// the desired names per-kube.Kind. Used by Services / Crons whose per-
// workload Secret has a derived name (`<name>-secrets`) distinct from
// the workload name itself.
//
// Any kube.Kind not present in perKubeKind is swept with desired=nil
// (which deletes everything for that kind under the owner). Pass an
// explicit empty slice via the map to keep nothing silently.
func SweepOwnedFor(ctx context.Context, kc *kube.Client, ns string, kind Kind, perKubeKind map[kube.Kind][]string) error {
	owner := kind.OwnerLabel()
	if owner == "" {
		return fmt.Errorf("provider.SweepOwnedFor: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	for _, kk := range kind.KubeKinds() {
		desired := perKubeKind[kk]
		if err := kc.SweepOwned(ctx, ns, owner, kk, desired); err != nil {
			return err
		}
	}
	return nil
}

// ListOwnedAll returns the union of names across every kube.Kind for
// `kind`. Used by the plan engine's existence-detection (does this
// workload exist on the cluster?) where we don't care which kube.Kind
// is hosting it. Result is sorted + deduped.
func ListOwnedAll(ctx context.Context, kc *kube.Client, ns string, kind Kind) ([]string, error) {
	owner := kind.OwnerLabel()
	if owner == "" {
		return nil, fmt.Errorf("provider.ListOwnedAll: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	seen := map[string]bool{}
	for _, kk := range kind.KubeKinds() {
		names, err := kc.ListOwned(ctx, ns, owner, kk)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// ListOwnedByKubeKind returns names per-kube.Kind — needed when the
// caller wants to diff a specific apiserver kind separately (e.g.,
// Services planner wants Deployment+StatefulSet for workload
// existence, but Secret for per-service Secret-key diff).
func ListOwnedByKubeKind(ctx context.Context, kc *kube.Client, ns string, kind Kind) (map[kube.Kind][]string, error) {
	owner := kind.OwnerLabel()
	if owner == "" {
		return nil, fmt.Errorf("provider.ListOwnedByKubeKind: kind %q has no owner label (layer=%s)", kind, kind.Layer())
	}
	out := map[kube.Kind][]string{}
	for _, kk := range kind.KubeKinds() {
		names, err := kc.ListOwned(ctx, ns, owner, kk)
		if err != nil {
			return nil, err
		}
		out[kk] = names
	}
	return out, nil
}
