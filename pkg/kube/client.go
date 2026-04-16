// KubeClient wraps client-go for native k8s API access. No kubectl, no SSH,
// no shell commands. Two constructors:
//   - NewLocal: agent on master, talks to localhost:6443
//   - NewTunneled: CLI bootstrap, routes through SSH tunnel to master:6443
package kube

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// KubeClient talks to the k8s API directly via client-go.
type KubeClient struct {
	cs     kubernetes.Interface
	dyn    dynamic.Interface
	mapper meta.RESTMapper
	config *rest.Config // needed for SPDY exec

	// ExecHook overrides ExecInPod for testing. When set, ExecInPod calls this
	// instead of SPDY. Production code never sets this.
	ExecHook func(ctx context.Context, ns, pod string, command []string, stdout, stderr io.Writer) error
}

// NewLocal creates a client from a kubeconfig file on the master.
// Used by the agent — direct access, no tunnel.
func NewLocal(kubeconfigPath string) (*KubeClient, error) {
	if kubeconfigPath == "" {
		kubeconfigPath = kubeconfigPath_
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("kube client from %s: %w", kubeconfigPath, err)
	}
	return fromConfig(config)
}

// NewTunneled creates a client that routes through an SSH connection.
// Used by CLI bootstrap — the laptop doesn't have direct access to the
// k8s API, so it tunnels through the SSH connection to the master.
func NewTunneled(ctx context.Context, ssh utils.SSHClient) (*KubeClient, error) {
	// Read kubeconfig from the master.
	out, err := ssh.Run(ctx, fmt.Sprintf("cat %s", kubeconfigPath_))
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig from master: %w", err)
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(out)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	// Route all API calls through the SSH tunnel.
	config.Dial = func(_ context.Context, _, _ string) (net.Conn, error) {
		return ssh.DialTCP(ctx, "127.0.0.1:6443")
	}
	return fromConfig(config)
}

var kubeconfigPath_ = "/home/deploy/.kube/config"

// NewFromClientset creates a KubeClient from an existing clientset.
// Used when the caller manages client construction (tests, custom configs).
func NewFromClientset(cs kubernetes.Interface) *KubeClient {
	return &KubeClient{cs: cs}
}

func fromConfig(config *rest.Config) (*KubeClient, error) {
	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("kube clientset: %w", err)
	}
	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("kube dynamic client: %w", err)
	}
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("kube discovery: %w", err)
	}
	groupResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, fmt.Errorf("kube api resources: %w", err)
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)
	return &KubeClient{cs: cs, dyn: dyn, mapper: mapper, config: config}, nil
}

// ── Namespace ───────────────────────────────────────────────────────────────

func (k *KubeClient) EnsureNamespace(ctx context.Context, ns string) error {
	_, err := k.cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("get namespace %s: %w", ns, err)
	}
	_, err = k.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create namespace %s: %w", ns, err)
	}
	return nil
}

// ── Apply YAML ──────────────────────────────────────────────────────────────

// Apply decodes a YAML document and applies it to the cluster.
// Handles multi-document YAML (separated by ---).
func (k *KubeClient) Apply(ctx context.Context, ns string, yamlDoc string) error {
	reader := yamlutil.NewYAMLReader(bufio.NewReader(strings.NewReader(yamlDoc)))
	for {
		raw, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read yaml doc: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		if err := k.applyOne(ctx, ns, raw); err != nil {
			return err
		}
	}
	return nil
}

// ApplyGlobal applies a YAML document without a namespace scope.
func (k *KubeClient) ApplyGlobal(ctx context.Context, yamlDoc string) error {
	return k.Apply(ctx, "", yamlDoc)
}

func (k *KubeClient) applyOne(ctx context.Context, ns string, raw []byte) error {
	// When running with a fake/test clientset (no dynamic client or mapper),
	// skip the apply — the typed API tests verify behavior directly.
	if k.dyn == nil || k.mapper == nil {
		return nil
	}

	decUnstructured := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme)
	obj := &unstructured.Unstructured{}
	_, gvk, err := decUnstructured.Decode(raw, nil, obj)
	if err != nil {
		return fmt.Errorf("decode yaml: %w", err)
	}

	mapping, err := k.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("resolve resource for %s: %w", gvk.Kind, err)
	}

	var client dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		effectiveNS := obj.GetNamespace()
		if effectiveNS == "" {
			effectiveNS = ns
		}
		if effectiveNS != "" {
			obj.SetNamespace(effectiveNS)
		}
		client = k.dyn.Resource(mapping.Resource).Namespace(effectiveNS)
	} else {
		client = k.dyn.Resource(mapping.Resource)
	}

	// Server-side apply with force conflicts.
	obj.SetManagedFields(nil)
	data, err := obj.MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	_, err = client.Patch(ctx, obj.GetName(), "application/apply-patch+yaml", data, metav1.PatchOptions{
		FieldManager: "nvoi",
		Force:        boolPtr(true),
	})
	if err != nil {
		return fmt.Errorf("apply %s/%s: %w", gvk.Kind, obj.GetName(), err)
	}
	return nil
}

// ── Secrets ─────────────────────────────────────────────────────────────────

func (k *KubeClient) UpsertSecretKey(ctx context.Context, ns, name, key, value string) error {
	secrets := k.cs.CoreV1().Secrets(ns)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// Create new secret.
		_, err = secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data:       map[string][]byte{key: []byte(value)},
		}, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return fmt.Errorf("get secret %s: %w", name, err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[key] = []byte(value)
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func (k *KubeClient) ListSecretKeys(ctx context.Context, ns, name string) ([]string, error) {
	secret, err := k.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (k *KubeClient) DeleteSecretKey(ctx context.Context, ns, name, key string) error {
	secrets := k.cs.CoreV1().Secrets(ns)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	delete(secret.Data, key)
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}

func (k *KubeClient) DeleteSecret(ctx context.Context, ns, name string) error {
	err := k.cs.CoreV1().Secrets(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubeClient) GetSecretValue(ctx context.Context, ns, name, key string) (string, error) {
	secret, err := k.cs.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	v, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in secret %s", key, name)
	}
	return string(v), nil
}

// ── Pods ────────────────────────────────────────────────────────────────────

func (k *KubeClient) GetAllPods(ctx context.Context, ns string) ([]PodInfo, error) {
	pods, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: NvoiSelector,
	})
	if err != nil {
		return nil, err
	}
	result := make([]PodInfo, len(pods.Items))
	for i, p := range pods.Items {
		ready := false
		var restarts int32
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Ready {
				ready = true
			}
			restarts += cs.RestartCount
		}
		status := string(p.Status.Phase)
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			}
		}
		result[i] = PodInfo{
			Name:   p.Name,
			Status: status,
			Ready:  ready,
		}
	}
	return result, nil
}

func (k *KubeClient) FirstPod(ctx context.Context, ns, service string) (string, error) {
	pods, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", utils.LabelAppName, service),
		Limit:         1,
	})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for %s", service)
	}
	return pods.Items[0].Name, nil
}

func (k *KubeClient) GetServicePort(ctx context.Context, ns, name string) (int, error) {
	svc, err := k.cs.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service %s has no ports", name)
	}
	return int(svc.Spec.Ports[0].Port), nil
}

// ── Delete ──────────────────────────────────────────────────────────────────

func (k *KubeClient) DeleteByName(ctx context.Context, ns, name string) error {
	// Try deployment, statefulset, service, cronjob, ingress — delete whichever exists.
	for _, fn := range []func() error{
		func() error { return k.cs.AppsV1().Deployments(ns).Delete(ctx, name, metav1.DeleteOptions{}) },
		func() error { return k.cs.AppsV1().StatefulSets(ns).Delete(ctx, name, metav1.DeleteOptions{}) },
		func() error { return k.cs.CoreV1().Services(ns).Delete(ctx, name, metav1.DeleteOptions{}) },
		func() error {
			return k.cs.NetworkingV1().Ingresses(ns).Delete(ctx, "ingress-"+name, metav1.DeleteOptions{})
		},
	} {
		err := fn()
		if err == nil || errors.IsNotFound(err) {
			continue
		}
		// Ignore errors — best-effort cleanup.
	}
	return nil
}

func (k *KubeClient) DeleteCronByName(ctx context.Context, ns, name string) error {
	err := k.cs.BatchV1().CronJobs(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// ── Nodes ───────────────────────────────────────────────────────────────────

func (k *KubeClient) DrainAndRemoveNode(ctx context.Context, nodeName string) error {
	// Cordon.
	node, err := k.cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get node %s: %w", nodeName, err)
	}
	node.Spec.Unschedulable = true
	if _, err := k.cs.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("cordon %s: %w", nodeName, err)
	}

	// Evict pods.
	pods, err := k.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return fmt.Errorf("list pods on %s: %w", nodeName, err)
	}
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		if err := k.cs.CoreV1().Pods(p.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{
			GracePeriodSeconds: int64Ptr(30),
		}); err != nil {
			return fmt.Errorf("evict pod %s/%s on %s: %w", p.Namespace, p.Name, nodeName, err)
		}
	}

	// Delete node.
	return k.cs.CoreV1().Nodes().Delete(ctx, nodeName, metav1.DeleteOptions{})
}

func (k *KubeClient) LabelNode(ctx context.Context, nodeName, role string) error {
	node, err := k.cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get node %s: %w", nodeName, err)
	}
	if node.Labels == nil {
		node.Labels = map[string]string{}
	}
	node.Labels["nvoi/role"] = role
	_, err = k.cs.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	return err
}

// ── Ingress ─────────────────────────────────────────────────────────────────

func (k *KubeClient) ApplyIngress(ctx context.Context, ns string, route IngressRoute, acme bool) error {
	yamlDoc, err := GenerateIngressYAML(route, ns, acme)
	if err != nil {
		return err
	}
	return k.Apply(ctx, ns, yamlDoc)
}

func (k *KubeClient) DeleteIngress(ctx context.Context, ns, service string) error {
	name := KubeIngressName(service)
	err := k.cs.NetworkingV1().Ingresses(ns).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *KubeClient) GetIngressRoutes(ctx context.Context, ns string) ([]IngressRoute, error) {
	list, err := k.cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var routes []IngressRoute
	for _, ing := range list.Items {
		svc := strings.TrimPrefix(ing.Name, "ingress-")
		var domains []string
		for _, rule := range ing.Spec.Rules {
			domains = append(domains, rule.Host)
		}
		var port int
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP != nil {
				for _, path := range rule.HTTP.Paths {
					if path.Backend.Service != nil {
						port = int(path.Backend.Service.Port.Number)
					}
				}
			}
		}
		routes = append(routes, IngressRoute{Service: svc, Domains: domains, Port: port})
	}
	return routes, nil
}

// ── Jobs ────────────────────────────────────────────────────────────────────

func (k *KubeClient) CreateJobFromCronJob(ctx context.Context, ns, cronName, jobName string) error {
	cron, err := k.cs.BatchV1().CronJobs(ns).Get(ctx, cronName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get cronjob %s: %w", cronName, err)
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: ns,
			Labels:    cron.Spec.JobTemplate.Labels,
		},
		Spec: cron.Spec.JobTemplate.Spec,
	}
	_, err = k.cs.BatchV1().Jobs(ns).Create(ctx, job, metav1.CreateOptions{})
	return err
}

func (k *KubeClient) WaitForJob(ctx context.Context, ns, jobName string, emitter ProgressEmitter) error {
	timeout := jobTimeout
	pollInterval := jobPollInterval
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		job, err := k.cs.BatchV1().Jobs(ns).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get job %s: %w", jobName, err)
		}
		for _, c := range job.Status.Conditions {
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return nil
			}
			if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
				return fmt.Errorf("job %s failed: %s", jobName, c.Message)
			}
		}
		if emitter != nil {
			emitter.Progress(fmt.Sprintf("job %s: %d active, %d succeeded, %d failed",
				jobName, job.Status.Active, job.Status.Succeeded, job.Status.Failed))
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("job %s: timed out after %s", jobName, timeout)
}

var jobTimeout = 5 * time.Minute
var jobPollInterval = 3 * time.Second

// ── Logs ────────────────────────────────────────────────────────────────────

func (k *KubeClient) StreamLogs(ctx context.Context, ns, podName string, opts *corev1.PodLogOptions, w io.Writer) error {
	stream, err := k.cs.CoreV1().Pods(ns).GetLogs(podName, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream logs %s: %w", podName, err)
	}
	defer stream.Close()
	_, err = io.Copy(w, stream)
	return err
}

func (k *KubeClient) RecentLogs(ctx context.Context, ns, podName string, tailLines int) string {
	t := int64(tailLines)
	stream, err := k.cs.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		TailLines: &t,
	}).Stream(ctx)
	if err != nil {
		return ""
	}
	defer stream.Close()
	data, _ := io.ReadAll(stream)
	return string(data)
}

// ── Rollout ─────────────────────────────────────────────────────────────────

// WaitRolloutReady polls until all pods for a workload are ready.
func (k *KubeClient) WaitRolloutReady(ctx context.Context, ns, name, kind string, hasHealthCheck bool, emitter ProgressEmitter) error {
	timeout := rolloutTimeout
	pollInterval := rolloutPollInterval
	deadline := time.Now().Add(timeout)

	selector := fmt.Sprintf("%s=%s", utils.LabelAppName, name)

	for time.Now().Before(deadline) {
		pods, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		total := len(pods.Items)
		ready := 0
		var issues []string
		for _, p := range pods.Items {
			podReady := false
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					podReady = true
				}
			}
			if podReady {
				ready++
				continue
			}
			for _, cs := range p.Status.ContainerStatuses {
				if cs.RestartCount > 2 {
					// Container is crash-looping — get logs and fail.
					logs := k.RecentLogs(ctx, ns, p.Name, 10)
					return fmt.Errorf("%s: container exited with code %d (restarts: %d)\nlogs:\n%s",
						name, exitCode(cs), cs.RestartCount, logs)
				}
				if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
					issues = append(issues, cs.State.Waiting.Reason)
				}
			}
		}

		if ready == total && total > 0 {
			return nil
		}

		status := fmt.Sprintf("%d/%d ready", ready, total)
		if len(issues) > 0 {
			status += " (" + strings.Join(issues, ", ") + ")"
		}
		if emitter != nil {
			emitter.Progress(fmt.Sprintf("%s: %s", name, status))
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("%s: rollout timed out after %s", name, timeout)
}

func exitCode(cs corev1.ContainerStatus) int32 {
	if cs.State.Terminated != nil {
		return cs.State.Terminated.ExitCode
	}
	if cs.LastTerminationState.Terminated != nil {
		return cs.LastTerminationState.Terminated.ExitCode
	}
	return -1
}

// ── Traefik ACME ────────────────────────────────────────────────────────────

func (k *KubeClient) EnsureTraefikACME(ctx context.Context, email string, acme bool) error {
	// Traefik is managed by k3s via HelmChartConfig. Apply the config to enable ACME.
	if !acme {
		return nil
	}
	yamlDoc := fmt.Sprintf(`apiVersion: helm.cattle.io/v1
kind: HelmChartConfig
metadata:
  name: traefik
  namespace: kube-system
spec:
  valuesContent: |
    certResolvers:
      letsencrypt:
        email: %s
        tlsChallenge: true
        storage: /data/acme.json`, email)
	return k.Apply(ctx, "kube-system", yamlDoc)
}

// WaitForTraefikReady polls until Traefik deployment is ready.
func (k *KubeClient) WaitForTraefikReady(ctx context.Context) error {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		dep, err := k.cs.AppsV1().Deployments("kube-system").Get(ctx, "traefik", metav1.GetOptions{})
		if err == nil && dep.Status.ReadyReplicas > 0 {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("traefik not ready after 2 minutes")
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func boolPtr(b bool) *bool    { return &b }
func int64Ptr(i int64) *int64 { return &i }

// GetJSON returns raw JSON for a resource query. Compatibility layer for
// callers that parse kubectl JSON output. Prefer typed methods where possible.
func (k *KubeClient) GetJSON(ctx context.Context, ns, resource, selector string) ([]byte, error) {
	// Map resource string to typed list call.
	switch resource {
	case "nodes":
		list, err := k.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "deployments", "deploy":
		list, err := k.cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "statefulsets", "sts":
		list, err := k.cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "pods":
		list, err := k.cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "services", "svc":
		list, err := k.cs.CoreV1().Services(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "cronjobs":
		list, err := k.cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	case "ingresses", "ingress":
		list, err := k.cs.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, err
		}
		return marshalJSON(list)
	default:
		return nil, fmt.Errorf("unsupported resource type: %s", resource)
	}
}

func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// ── Exec ────────────────────────────────────────────────────────────────────

// ExecInPod runs a command in a pod's first container and streams output.
// Uses SPDY remotecommand — no kubectl binary, no SSH.
func (k *KubeClient) ExecInPod(ctx context.Context, ns, podName string, command []string, stdout, stderr io.Writer) error {
	if k.ExecHook != nil {
		return k.ExecHook(ctx, ns, podName, command, stdout, stderr)
	}
	if k.config == nil {
		return fmt.Errorf("exec requires a real cluster connection (not available in test mode)")
	}
	req := k.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: command,
			Stdout:  stdout != nil,
			Stderr:  stderr != nil,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(k.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("exec %s: %w", podName, err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})
}
