package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/kube"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
)

// describeDatabaseProbeTimeout caps each ExecSQL("SELECT 1") so describe
// stays snappy when one DB is unreachable. 3s is enough for cross-region
// SaaS hops + cold-start auth, short enough that a wedged probe doesn't
// block the read-only command. Probes run in parallel — total wall-time
// ≈ slowest probe, not sum.
const describeDatabaseProbeTimeout = 3 * time.Second

// ── Request / Result types ──────────────────────────────────────────────────────

type DescribeRequest struct {
	Cluster
	Cfg          provider.ProviderConfigView // forwards to Cluster.Kube for on-demand connect
	StorageNames []string                    // from cfg — config is the source of truth
	// Services / Crons are the cfg-declared workload names. The SECRETS
	// section uses these to classify owner (service:X vs cron:X) when a
	// Secret matches the `{name}-secrets` pattern. Both are passed
	// because the in-cluster Secret name doesn't carry the kind.
	Services []string
	Crons    []string
	// Databases is one DatabaseProbe per cfg.Databases entry, pre-
	// resolved at the cmd boundary so describe can run a parallel
	// live ExecSQL ping against each. Empty when cfg has no databases.
	Databases []DatabaseProbe
}

// DatabaseProbe carries everything Describe needs to render one row of
// the DATABASES section + run a 3-second live SELECT 1. Resolution
// happens at the cmd boundary (where credentials are available); core
// just calls Provider.ExecSQL through the closure.
type DatabaseProbe struct {
	Name     string                    // cfg.Databases key (e.g. "main")
	Engine   string                    // "postgres" | "neon" | "planetscale"
	Provider provider.DatabaseProvider // resolved with creds
	Request  provider.DatabaseRequest  // full request (Kube, Namespace, PodName, Spec, …)
}

type DescribeNode struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Role   string `json:"role"`
	IP     string `json:"ip"`
}

type DescribeWorkload struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`  // "deployment" or "statefulset"
	Ready string `json:"ready"` // "2/2"
	Image string `json:"image"`
	Age   string `json:"age"`
}

type DescribeCron struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Image    string `json:"image"`
	Age      string `json:"age"`
	Status   string `json:"status"`
}

type DescribePod struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Node     string `json:"node"`
	Restarts int    `json:"restarts"`
	Age      string `json:"age"`
}

type DescribeService struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	ClusterIP string `json:"cluster_ip"`
	Ports     string `json:"ports"`
}

type DescribeIngress struct {
	Domain  string `json:"domain"`
	Service string `json:"service"`
	Port    int    `json:"port"`
}

// DescribeSecret is one row of the SECRETS section — one row per
// nvoi-managed Secret object in the namespace, with all its keys.
//
// Owner classifications (derived from the Secret's name):
//   - "service:X"        — `{X}-secrets`, X is in cfg.Services
//   - "cron:X"           — `{X}-secrets`, X is in cfg.Crons
//   - "workload:X"       — `{X}-secrets`, X not in cfg (orphan from a prior deploy)
//   - "database:X"       — `{base}-db-{X}-credentials`
//   - "database:X:bk"    — `{base}-db-{X}-backup-creds`
//   - "registry"         — `registry-auth` (dockerconfigjson for image pulls)
//   - "tunnel:cloudflare"/`tunnel:ngrok` — agent auth Secrets
//   - ""                 — unknown nvoi-managed Secret (shows up with empty owner)
type DescribeSecret struct {
	Name  string   `json:"name"`
	Owner string   `json:"owner"`
	Keys  []string `json:"keys"`
}

// DescribeDatabase is one row of the DATABASES section. Engine-agnostic
// columns: NAME, ENGINE, ENDPOINT, STATE (cluster-derived), LIVE (probe).
//
// State values:
//   - "Ready 1/1"      — selfhosted StatefulSet readiness
//   - "Ready"          — SaaS, credentials Secret present with non-empty url
//   - "Not reconciled" — credentials Secret absent (deploy hasn't run, or DB step errored)
//
// Live values:
//   - "Up"             — ExecSQL("SELECT 1") returned ok within timeout
//   - "Down: <reason>" — ExecSQL errored; short reason from the provider
//   - "—"              — probe skipped (Not reconciled, or no probe configured)
type DescribeDatabase struct {
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Endpoint string `json:"endpoint"`
	State    string `json:"state"`
	Live     string `json:"live"`
}

type DescribeResult struct {
	Namespace string             `json:"namespace"`
	Nodes     []DescribeNode     `json:"nodes"`
	Workloads []DescribeWorkload `json:"workloads"`
	Pods      []DescribePod      `json:"pods"`
	Services  []DescribeService  `json:"services"`
	Crons     []DescribeCron     `json:"crons"`
	Databases []DescribeDatabase `json:"databases"`
	Ingress   []DescribeIngress  `json:"ingress"`
	Secrets   []DescribeSecret   `json:"secrets"`
	Storage   []StorageItem      `json:"storage"`
}

// ── Public ──────────────────────────────────────────────────────────────────────

func Describe(ctx context.Context, req DescribeRequest) (*DescribeResult, error) {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ns := names.KubeNamespace()

	result := &DescribeResult{Namespace: ns}
	result.Nodes = describeNodes(ctx, kc)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Workloads = describeWorkloads(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Pods = describePods(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Services = describeServices(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Crons = describeCrons(ctx, kc, ns)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Databases = describeDatabases(ctx, kc, ns, req.Databases)
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	// Ingress section: only meaningful on the Caddy path. When
	// providers.tunnel is set, ingress lives at the provider edge —
	// `nvoi resources` surfaces it. describe is cluster-scope and
	// stays silent here. Caddy might not be running yet (first deploy
	// in progress) — that's not an error for describe; the routes
	// list just stays empty.
	routes, err := kc.GetCaddyRoutes(ctx)
	if err == nil {
		for _, r := range routes {
			for _, d := range r.Domains {
				result.Ingress = append(result.Ingress, DescribeIngress{
					Domain: d, Service: r.Service, Port: r.Port,
				})
			}
		}
	}

	// Storage — derived from config, not from scanning k8s secrets
	for _, storageName := range req.StorageNames {
		result.Storage = append(result.Storage, StorageItem{
			Name:   storageName,
			Bucket: names.Bucket(storageName),
		})
	}

	// Secrets — list every nvoi-managed Secret in the namespace, then
	// classify by name pattern. This is more honest than walking the
	// per-workload `{name}-secrets` list: the cluster also holds
	// database credentials Secrets (`{db}-credentials`), backup-creds
	// Secrets, the dockerconfigjson pull Secret (`registry-auth`), and
	// tunnel-agent auth Secrets (`cloudflared-token` / `ngrok-authtoken`).
	// All real cluster-side state, all worth surfacing to the operator.
	result.Secrets = describeSecrets(ctx, kc, ns, names, req.Services, req.Crons)

	return result, nil
}

// DescribeJSON returns raw JSON for each kube resource type, preserving the
// shape clients of the legacy command depended on.
func DescribeJSON(ctx context.Context, req DescribeRequest) (map[string]json.RawMessage, error) {
	kc, names, cleanup, err := req.Cluster.Kube(ctx, req.Cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ns := names.KubeNamespace()
	sel := kube.NvoiSelector
	result := map[string]json.RawMessage{}

	type query struct {
		key string
		fn  func() (any, error)
	}
	queries := []query{
		{"nodes", func() (any, error) {
			return kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		}},
		{"deployments", func() (any, error) {
			return kc.Clientset().AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"statefulsets", func() (any, error) {
			return kc.Clientset().AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"pods", func() (any, error) {
			return kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"services", func() (any, error) {
			return kc.Clientset().CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"cronjobs", func() (any, error) {
			return kc.Clientset().BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
		{"ingresses", func() (any, error) {
			return kc.Clientset().NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
		}},
	}

	for _, q := range queries {
		obj, err := q.fn()
		if err != nil {
			continue
		}
		if data, err := json.Marshal(obj); err == nil && len(data) > 0 {
			result[q.key] = data
		}
	}
	return result, nil
}

// ── Internal helpers ────────────────────────────────────────────────────────────

func describeNodes(ctx context.Context, kc *kube.Client) []DescribeNode {
	nodes, err := kc.Clientset().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	out := make([]DescribeNode, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		status := "NotReady"
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				status = "Ready"
			}
		}
		ip := ""
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP {
				ip = a.Address
				break
			}
		}
		out = append(out, DescribeNode{
			Name:   n.Name,
			Status: status,
			Role:   n.Labels[utils.LabelNvoiRole],
			IP:     ip,
		})
	}
	return out
}

func describeWorkloads(ctx context.Context, kc *kube.Client, ns string) []DescribeWorkload {
	var out []DescribeWorkload

	deps, err := kc.Clientset().AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err == nil {
		for _, d := range deps.Items {
			image := ""
			if len(d.Spec.Template.Spec.Containers) > 0 {
				image = d.Spec.Template.Spec.Containers[0].Image
			}
			replicas := int32(0)
			if d.Spec.Replicas != nil {
				replicas = *d.Spec.Replicas
			}
			out = append(out, DescribeWorkload{
				Name:  d.Name,
				Kind:  "deployment",
				Ready: fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, replicas),
				Image: image,
				Age:   utils.HumanAge(d.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			})
		}
	}

	ss, err := kc.Clientset().AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err == nil {
		for _, s := range ss.Items {
			image := ""
			if len(s.Spec.Template.Spec.Containers) > 0 {
				image = s.Spec.Template.Spec.Containers[0].Image
			}
			replicas := int32(0)
			if s.Spec.Replicas != nil {
				replicas = *s.Spec.Replicas
			}
			out = append(out, DescribeWorkload{
				Name:  s.Name,
				Kind:  "statefulset",
				Ready: fmt.Sprintf("%d/%d", s.Status.ReadyReplicas, replicas),
				Image: image,
				Age:   utils.HumanAge(s.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			})
		}
	}
	return out
}

func describeCrons(ctx context.Context, kc *kube.Client, ns string) []DescribeCron {
	list, err := kc.Clientset().BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		return nil
	}
	out := make([]DescribeCron, 0, len(list.Items))
	for _, c := range list.Items {
		status := "idle"
		if len(c.Status.Active) > 0 {
			status = "active"
		} else if c.Status.LastScheduleTime != nil {
			status = "scheduled"
		}
		image := ""
		if len(c.Spec.JobTemplate.Spec.Template.Spec.Containers) > 0 {
			image = c.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image
		}
		out = append(out, DescribeCron{
			Name:     c.Name,
			Schedule: c.Spec.Schedule,
			Image:    image,
			Age:      utils.HumanAge(c.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
			Status:   status,
		})
	}
	return out
}

func describePods(ctx context.Context, kc *kube.Client, ns string) []DescribePod {
	pods, err := kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil
		}
		return nil
	}
	out := make([]DescribePod, 0, len(pods.Items))
	for _, p := range pods.Items {
		status := string(p.Status.Phase)
		restarts := 0
		for _, cs := range p.Status.ContainerStatuses {
			restarts += int(cs.RestartCount)
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			}
		}
		out = append(out, DescribePod{
			Name:     p.Name,
			Status:   status,
			Node:     p.Spec.NodeName,
			Restarts: restarts,
			Age:      utils.HumanAge(p.CreationTimestamp.Format("2006-01-02T15:04:05Z")),
		})
	}
	return out
}

// describeDatabases populates one DescribeDatabase row per probe.
// Cluster-derived state + a parallel ExecSQL("SELECT 1") liveness ping
// per DB, each capped at describeDatabaseProbeTimeout.
//
// State derivation:
//   - credentials Secret missing → "Not reconciled", probe skipped (Live="—")
//   - selfhosted (postgres): StatefulSet ReadyReplicas/Replicas → "Ready X/Y"
//   - SaaS (neon, planetscale): credentials Secret present with non-empty
//     `url` → "Ready"; the Secret IS the proof of provider-side state
func describeDatabases(ctx context.Context, kc *kube.Client, ns string, probes []DatabaseProbe) []DescribeDatabase {
	if len(probes) == 0 {
		return nil
	}
	out := make([]DescribeDatabase, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		i, p := i, p
		// Synchronous cluster-side reads (cheap; no outbound). Sets
		// Endpoint + State + a default Live="—" — the parallel probe
		// below overwrites Live when it's worth running.
		out[i] = describeDatabaseFromCluster(ctx, kc, ns, p)
		if out[i].State == "Not reconciled" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i].Live = probeDatabaseLive(ctx, p)
		}()
	}
	wg.Wait()
	return out
}

// describeDatabaseFromCluster populates the cluster-derived fields
// (Endpoint, State) without reaching out to any provider API. Read-only
// against the kube apiserver; never blocks longer than a Get.
func describeDatabaseFromCluster(ctx context.Context, kc *kube.Client, ns string, p DatabaseProbe) DescribeDatabase {
	row := DescribeDatabase{Name: p.Name, Engine: p.Engine, Live: "—"}

	credsName := p.Request.CredentialsSecretName
	if credsName == "" {
		row.State = "Not reconciled"
		return row
	}
	credsSecret, err := kc.Clientset().CoreV1().Secrets(ns).Get(ctx, credsName, metav1.GetOptions{})
	if err != nil || credsSecret == nil {
		row.State = "Not reconciled"
		return row
	}
	url := string(credsSecret.Data["url"])
	host := string(credsSecret.Data["host"])
	port := string(credsSecret.Data["port"])
	if url == "" {
		row.State = "Not reconciled"
		return row
	}
	if host != "" && port != "" {
		row.Endpoint = fmt.Sprintf("%s:%s", host, port)
	} else {
		row.Endpoint = host
	}

	// Selfhosted: defer to StatefulSet readiness for the State column —
	// "Ready 1/1" is more precise than the Secret's mere presence
	// (Secret is created early in Reconcile; StatefulSet readiness
	// reflects pod availability).
	if p.Request.PodName != "" {
		stsName := strings.TrimSuffix(p.Request.PodName, "-0")
		ss, err := kc.Clientset().AppsV1().StatefulSets(ns).Get(ctx, stsName, metav1.GetOptions{})
		if err == nil && ss != nil {
			replicas := int32(0)
			if ss.Spec.Replicas != nil {
				replicas = *ss.Spec.Replicas
			}
			row.State = fmt.Sprintf("Ready %d/%d", ss.Status.ReadyReplicas, replicas)
			return row
		}
		// StatefulSet missing but credentials Secret present is the
		// edge case where reconcile got partway through. Treat as
		// not-yet-ready rather than not-reconciled (the Secret IS
		// reconciled state).
		row.State = "Pending"
		return row
	}

	// SaaS: Secret presence is the reconciliation signal. Per-provider
	// branching (Neon project state, PlanetScale db state) belongs in
	// `nvoi resources`, not here.
	row.State = "Ready"
	return row
}

// probeDatabaseLive runs ExecSQL("SELECT 1") with a hard timeout and
// returns the Live column value. Errors are short-summarized — the
// whole reason for the column is "is the DB reachable RIGHT NOW", not
// a full diagnostic dump.
func probeDatabaseLive(parent context.Context, p DatabaseProbe) string {
	if p.Provider == nil {
		return "—"
	}
	ctx, cancel := context.WithTimeout(parent, describeDatabaseProbeTimeout)
	defer cancel()

	if _, err := p.Provider.ExecSQL(ctx, p.Request, "SELECT 1"); err != nil {
		// Trim the wrapper noise so the table column stays readable.
		// "postgres.ExecSQL: rpc error: ..." → "rpc error: ..."
		msg := err.Error()
		if i := strings.Index(msg, ": "); i > 0 && i < 30 {
			msg = msg[i+2:]
		}
		if len(msg) > 60 {
			msg = msg[:57] + "..."
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "Down: timeout"
		}
		return "Down: " + msg
	}
	return "Up"
}

// describeSecrets lists every Secret in the namespace and returns one
// DescribeSecret per nvoi-recognized object, with keys sorted
// alphabetically. Owner is classified from the Secret's name against
// well-known patterns (see DescribeSecret docs). Caller passes service +
// cron name lists from cfg so {X}-secrets gets the right owner kind
// ("service" vs "cron"); a missing match falls back to "workload:X"
// (orphan from a prior deploy).
//
// We do NOT filter by NvoiSelector at the apiserver. Several of nvoi's
// historical Secret creation paths (kube.EnsureSecret, the per-DB
// credentials Secret, the workload {X}-secrets) didn't stamp the
// `app.kubernetes.io/managed-by=nvoi` label, and existing operators
// running against pre-fix clusters would lose visibility on those rows
// after a label-only deploy. Instead: list everything, then filter on
// classifySecretOwner(name) — empty Owner means "not a known nvoi
// pattern" and the row is dropped (k8s system Secrets, user-created
// Secrets unrelated to nvoi). Forward-compat is via EnsureSecret which
// now stamps the label too, so resources / future tooling can rely on
// it without breaking describe today.
//
// Empty list returns nil; List errors return nil and are silently
// dropped (consistent with the rest of describe).
func describeSecrets(ctx context.Context, kc *kube.Client, ns string, names *utils.Names, services, crons []string) []DescribeSecret {
	list, err := kc.Clientset().CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	if len(list.Items) == 0 {
		return nil
	}

	svcSet := make(map[string]bool, len(services))
	for _, s := range services {
		svcSet[s] = true
	}
	cronSet := make(map[string]bool, len(crons))
	for _, c := range crons {
		cronSet[c] = true
	}
	base := names.Base()

	out := make([]DescribeSecret, 0, len(list.Items))
	for _, s := range list.Items {
		owner := classifySecretOwner(s.Name, base, svcSet, cronSet)
		if owner == "" {
			// Not a known nvoi pattern — k8s system Secret (default
			// service-account token, etc.) or operator-created.
			// describe stays scoped to nvoi-recognized state.
			continue
		}
		keys := make([]string, 0, len(s.Data))
		for k := range s.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		out = append(out, DescribeSecret{
			Name:  s.Name,
			Owner: owner,
			Keys:  keys,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// classifySecretOwner derives the OWNER column from a Secret's name.
// String-pattern based; no provider knowledge needed beyond the project
// name prefix. Patterns must stay in sync with the helpers in
// pkg/utils/naming.go (KubeServiceSecrets, KubeDatabaseCredentials,
// KubeDatabaseBackupCreds) and the well-known constants in
// pkg/kube/registry.go (PullSecretName) and pkg/kube/tunnel.go
// (CloudflareTunnelSecretName / NgrokTunnelSecretName).
func classifySecretOwner(name, base string, services, crons map[string]bool) string {
	switch name {
	case kube.PullSecretName:
		return "registry"
	case kube.CloudflareTunnelSecretName:
		return "tunnel:cloudflare"
	case kube.NgrokTunnelSecretName:
		return "tunnel:ngrok"
	}
	// Database credentials: `{base}-db-{X}-credentials`
	if dbPrefix := base + "-db-"; strings.HasPrefix(name, dbPrefix) {
		rest := strings.TrimPrefix(name, dbPrefix)
		if strings.HasSuffix(rest, "-credentials") {
			return "database:" + strings.TrimSuffix(rest, "-credentials")
		}
		if strings.HasSuffix(rest, "-backup-creds") {
			return "database:" + strings.TrimSuffix(rest, "-backup-creds") + ":bk"
		}
	}
	// Workload secrets: `{X}-secrets`. Distinguish service vs cron via
	// cfg-derived sets; an unmatched X means the cfg lost a workload
	// it had on a prior deploy (orphan).
	if strings.HasSuffix(name, "-secrets") {
		short := strings.TrimSuffix(name, "-secrets")
		if services[short] {
			return "service:" + short
		}
		if crons[short] {
			return "cron:" + short
		}
		return "workload:" + short
	}
	return ""
}

func describeServices(ctx context.Context, kc *kube.Client, ns string) []DescribeService {
	svcs, err := kc.Clientset().CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: kube.NvoiSelector})
	if err != nil {
		return nil
	}
	out := make([]DescribeService, 0, len(svcs.Items))
	for _, s := range svcs.Items {
		ports := make([]string, 0, len(s.Spec.Ports))
		for _, p := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		out = append(out, DescribeService{
			Name:      s.Name,
			Type:      string(s.Spec.Type),
			ClusterIP: s.Spec.ClusterIP,
			Ports:     strings.Join(ports, ","),
		})
	}
	return out
}
