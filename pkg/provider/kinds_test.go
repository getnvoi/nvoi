package provider

import (
	"testing"

	"github.com/getnvoi/nvoi/pkg/kube"
)

// TestKindRegistry_Complete is a guard against silently adding a Kind
// constant without a kindMeta row (or vice versa). init() panics on
// load if this is broken — so this test passing means init() didn't
// panic, which means the registry is complete. Lock it explicitly so a
// future maintainer doesn't think init()-only checks are enough.
func TestKindRegistry_Complete(t *testing.T) {
	for _, k := range AllKinds() {
		if _, ok := kindRegistry[k]; !ok {
			t.Errorf("Kind %q has no kindMeta row", k)
		}
	}
	for k := range kindRegistry {
		found := false
		for _, kk := range AllKinds() {
			if k == kk {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("kindRegistry has row for unknown Kind %q", k)
		}
	}
}

// TestKind_LayerAssignment locks the LayerInfra / LayerCluster split
// per CLAUDE.md's two-layer-core model. A Kind moving across layers is
// a load-bearing change (Deploy uses HasInfraChanges() for the loud/
// quiet branch); this test makes such moves explicit.
func TestKind_LayerAssignment(t *testing.T) {
	infra := []Kind{
		KindServer, KindFirewall, KindFirewallRule, KindVolume,
		KindNetwork, KindDNSRecord, KindBucket, KindTunnel,
	}
	cluster := []Kind{
		KindNamespace, KindRegistrySecret, KindServiceWorkload,
		KindCronWorkload, KindSecretKey, KindCaddyIngress,
		KindTunnelAgent, KindDatabase, // Database lives cluster-side
	}
	for _, k := range infra {
		if k.Layer() != LayerInfra {
			t.Errorf("%s: expected LayerInfra, got %s", k, k.Layer())
		}
		if !k.IsInfra() || k.IsCluster() {
			t.Errorf("%s: IsInfra/IsCluster mismatch with Layer()", k)
		}
	}
	for _, k := range cluster {
		if k.Layer() != LayerCluster {
			t.Errorf("%s: expected LayerCluster, got %s", k, k.Layer())
		}
		if !k.IsCluster() || k.IsInfra() {
			t.Errorf("%s: IsInfra/IsCluster mismatch with Layer()", k)
		}
	}
}

// TestKind_OwnerLabels enumerates the cluster-side Kinds and locks
// their owner-label values — these are persisted on every k8s object
// nvoi creates, so changes here are migrations not refactors.
func TestKind_OwnerLabels(t *testing.T) {
	cases := map[Kind]string{
		KindServer:          "",
		KindFirewall:        "",
		KindBucket:          "",
		KindNamespace:       "",
		KindRegistrySecret:  "registries",
		KindServiceWorkload: "services",
		KindCronWorkload:    "crons",
		KindSecretKey:       "services", // sub-resource of services
		KindCaddyIngress:    "caddy",
		KindTunnelAgent:     "tunnel",
		KindDatabase:        "databases",
	}
	for k, want := range cases {
		if got := k.OwnerLabel(); got != want {
			t.Errorf("%s.OwnerLabel() = %q, want %q", k, got, want)
		}
	}
}

// TestKind_KubeKinds_ServiceWorkload ensures the apiserver kinds
// reconcile.Services touches are exactly what the table declares.
// Adding a new kube.Kind to ServiceWorkload's reconcile path requires
// updating both this test and the kindRegistry row → makes the change
// reviewable as a single intent.
func TestKind_KubeKinds_ServiceWorkload(t *testing.T) {
	got := KindServiceWorkload.KubeKinds()
	want := []kube.Kind{kube.KindDeployment, kube.KindStatefulSet, kube.KindService, kube.KindSecret}
	if len(got) != len(want) {
		t.Fatalf("KindServiceWorkload.KubeKinds() len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, kk := range want {
		if got[i] != kk {
			t.Errorf("KubeKinds[%d] = %s, want %s", i, got[i], kk)
		}
	}
}

func TestKindFromOwnerLabel(t *testing.T) {
	cases := []struct {
		label string
		want  Kind
		ok    bool
	}{
		{"services", KindServiceWorkload, true}, // parent wins over KindSecretKey
		{"crons", KindCronWorkload, true},
		{"databases", KindDatabase, true},
		{"caddy", KindCaddyIngress, true},
		{"tunnel", KindTunnelAgent, true},
		{"registries", KindRegistrySecret, true},
		{"unknown-owner", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			got, ok := KindFromOwnerLabel(tc.label)
			if got != tc.want || ok != tc.ok {
				t.Errorf("KindFromOwnerLabel(%q) = (%q, %v), want (%q, %v)", tc.label, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestKind_InfraKindsHaveNoKubeKinds is a structural invariant —
// infra-side Kinds shouldn't carry a KubeKinds list (they don't write
// to the apiserver). Catches accidental cross-wiring.
func TestKind_InfraKindsHaveNoKubeKinds(t *testing.T) {
	for _, k := range AllKinds() {
		if k.Layer() != LayerInfra {
			continue
		}
		if len(k.KubeKinds()) != 0 {
			t.Errorf("infra-layer Kind %s has KubeKinds %v — infra kinds don't write to apiserver", k, k.KubeKinds())
		}
		if k.OwnerLabel() != "" {
			t.Errorf("infra-layer Kind %s has OwnerLabel %q — owner labels are k8s-only", k, k.OwnerLabel())
		}
	}
}
