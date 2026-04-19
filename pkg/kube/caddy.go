package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// Caddy timing — overridable for tests via SetCaddyTimingForTest. Production
// values bracket Let's Encrypt's typical ACME HTTP-01 latency (issuance is
// usually 5–30s; we wait up to 10 minutes to absorb DNS propagation lag and
// LE rate-limit retries).
var (
	caddyPollInterval = 3 * time.Second
	caddyCertTimeout  = 10 * time.Minute
	caddyHTTPSTimeout = 5 * time.Minute
	caddyReadyTimeout = 2 * time.Minute
)

// SetCaddyTimingForTest collapses every Caddy poll loop to fast intervals
// for tests. Returns a cleanup that restores production values.
func SetCaddyTimingForTest(poll, total time.Duration) func() {
	origPoll := caddyPollInterval
	origCert := caddyCertTimeout
	origHTTPS := caddyHTTPSTimeout
	origReady := caddyReadyTimeout
	caddyPollInterval = poll
	caddyCertTimeout = total
	caddyHTTPSTimeout = total
	caddyReadyTimeout = total
	return func() {
		caddyPollInterval = origPoll
		caddyCertTimeout = origCert
		caddyHTTPSTimeout = origHTTPS
		caddyReadyTimeout = origReady
	}
}

// EnsureCaddy idempotently applies the four Caddy resources (PVC,
// ConfigMap, Service, Deployment) in kube-system and waits for the
// Deployment to be Ready.
//
// Runs every reconcile — zero drift. If the user (or some other operator)
// mutates a field, the next deploy reconciles it back. Apply is typed
// Get→Create-if-missing→Update so a no-op deploy makes no API writes
// beyond the GETs.
func (c *Client) EnsureCaddy(ctx context.Context) error {
	if err := c.Apply(ctx, CaddyNamespace, buildCaddyPVC()); err != nil {
		return fmt.Errorf("ensure caddy pvc: %w", err)
	}
	if err := c.Apply(ctx, CaddyNamespace, buildCaddyConfigMap()); err != nil {
		return fmt.Errorf("ensure caddy configmap: %w", err)
	}
	if err := c.Apply(ctx, CaddyNamespace, buildCaddyService()); err != nil {
		return fmt.Errorf("ensure caddy service: %w", err)
	}
	if err := c.Apply(ctx, CaddyNamespace, buildCaddyDeployment()); err != nil {
		return fmt.Errorf("ensure caddy deployment: %w", err)
	}
	return c.waitForCaddyReady(ctx)
}

// waitForCaddyReady polls until the Caddy Deployment in kube-system reports
// all replicas Available.
func (c *Client) waitForCaddyReady(ctx context.Context) error {
	return utils.Poll(ctx, caddyPollInterval, caddyReadyTimeout, func() (bool, error) {
		dep, err := c.cs.AppsV1().Deployments(CaddyNamespace).Get(ctx, CaddyName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil // controller hasn't reconciled yet — retry
			}
			return false, nil
		}
		desired := int32(0)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		return desired > 0 && dep.Status.ReadyReplicas == desired, nil
	})
}

// ReloadCaddyConfig POSTs configJSON to the Caddy admin API at
// localhost:2019/load via Exec into the Caddy pod. Caddy validates the
// config first; on success the listeners atomically swap to the new routes
// without dropping live connections. On failure (4xx/5xx) the curl prints
// Caddy's error body to stdout and exits non-zero — we capture both and
// surface the message verbatim.
func (c *Client) ReloadCaddyConfig(ctx context.Context, configJSON []byte) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}

	var stdout, stderr bytes.Buffer
	execErr := c.Exec(ctx, ExecRequest{
		Namespace: CaddyNamespace,
		Pod:       pod,
		Container: CaddyName,
		Command: []string{"sh", "-c",
			"curl --fail-with-body -sS -X POST --data-binary @- " +
				"-H 'Content-Type: application/json' " +
				"http://" + CaddyAdminListen + "/load",
		},
		Stdin:  bytes.NewReader(configJSON),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if execErr != nil {
		body := strings.TrimSpace(stdout.String())
		errOut := strings.TrimSpace(stderr.String())
		switch {
		case body != "":
			return fmt.Errorf("caddy reload rejected: %s", body)
		case errOut != "":
			return fmt.Errorf("caddy reload: %s: %w", errOut, execErr)
		default:
			return fmt.Errorf("caddy reload: %w", execErr)
		}
	}
	return nil
}

// WaitForCaddyCert polls the Caddy pod until the ACME cert file for domain
// exists on /data with non-zero size. Indicates Caddy completed the ACME
// HTTP-01 flow and persisted the cert chain.
//
// Path: /data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<domain>/<domain>.crt
//
// Times out per caddyCertTimeout. Caller treats timeout as "warn and move
// on" — next deploy will re-verify, and Caddy keeps retrying ACME regardless.
func (c *Client) WaitForCaddyCert(ctx context.Context, domain string) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return err
	}
	certPath := fmt.Sprintf(
		"%s/caddy/certificates/acme-v02.api.letsencrypt.org-directory/%s/%s.crt",
		CaddyDataDir, domain, domain,
	)
	cmd := []string{"sh", "-c", fmt.Sprintf("test -s %q", certPath)}

	return utils.Poll(ctx, caddyPollInterval, caddyCertTimeout, func() (bool, error) {
		err := c.Exec(ctx, ExecRequest{
			Namespace: CaddyNamespace,
			Pod:       pod,
			Container: CaddyName,
			Command:   cmd,
			Stdout:    io.Discard,
			Stderr:    io.Discard,
		})
		return err == nil, nil
	})
}

// WaitForCaddyHTTPS curls https://<domain><healthPath> from inside the
// Caddy pod and waits for any non-5xx response. Run from the pod (not the
// operator's box) so we don't depend on the operator's local DNS — the
// pod resolves through k8s/upstream DNS that the public also uses.
//
// 401/403 = success: app is up, just rejecting unauthenticated probes.
// Connection failure (curl exits with code 000) → retry. 5xx → retry.
func (c *Client) WaitForCaddyHTTPS(ctx context.Context, domain, healthPath string) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return err
	}
	if healthPath == "" {
		healthPath = "/"
	}
	url := fmt.Sprintf("https://%s%s", domain, healthPath)
	// shell snippet: capture status, fail on 0xx/5xx, succeed otherwise.
	script := fmt.Sprintf(
		`code=$(curl -s --connect-timeout 5 -o /dev/null -w '%%{http_code}' %q); `+
			`case "$code" in 5*|0*) exit 1 ;; *) exit 0 ;; esac`,
		url,
	)
	cmd := []string{"sh", "-c", script}

	return utils.Poll(ctx, caddyPollInterval, caddyHTTPSTimeout, func() (bool, error) {
		err := c.Exec(ctx, ExecRequest{
			Namespace: CaddyNamespace,
			Pod:       pod,
			Container: CaddyName,
			Command:   cmd,
			Stdout:    io.Discard,
			Stderr:    io.Discard,
		})
		return err == nil, nil
	})
}

// GetCaddyRoutes pulls the live Caddy config from the admin API and
// translates the loaded routes back into the [service, port, domains]
// triples nvoi describe shows. Returns nil (no error) when the Caddy pod
// isn't up yet — describe should not fail just because the cluster has no
// ingress yet.
//
// The dial format produced by BuildCaddyConfig is
//
//	<service>.<namespace>.svc.cluster.local:<port>
//
// so the parse is a straight reverse of that.
func (c *Client) GetCaddyRoutes(ctx context.Context) ([]CaddyRoute, error) {
	pod, err := c.FirstPod(ctx, CaddyNamespace, CaddyName)
	if err != nil {
		// Caddy not running yet — no routes to report.
		return nil, nil
	}

	var stdout bytes.Buffer
	cmd := []string{"sh", "-c", "curl -sf http://" + CaddyAdminListen + "/config/apps/http/servers/main/routes"}
	execErr := c.Exec(ctx, ExecRequest{
		Namespace: CaddyNamespace,
		Pod:       pod,
		Container: CaddyName,
		Command:   cmd,
		Stdout:    &stdout,
		Stderr:    io.Discard,
	})
	if execErr != nil {
		return nil, nil // admin API not reachable / no routes loaded — describe degrades gracefully
	}
	body := bytes.TrimSpace(stdout.Bytes())
	if len(body) == 0 || string(body) == "null" {
		return nil, nil
	}

	var raw []caddyRoute
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse caddy routes: %w", err)
	}

	out := make([]CaddyRoute, 0, len(raw))
	for _, r := range raw {
		if len(r.Match) == 0 || len(r.Handle) == 0 {
			continue
		}
		var dial string
		for _, h := range r.Handle {
			if len(h.Upstreams) > 0 {
				dial = h.Upstreams[0].Dial
				break
			}
		}
		if dial == "" {
			continue
		}
		svc, port := parseCaddyDial(dial)
		if svc == "" || port == 0 {
			continue
		}
		// Each route has one Match block with the full host list.
		out = append(out, CaddyRoute{
			Service: svc,
			Port:    port,
			Domains: append([]string(nil), r.Match[0].Host...),
		})
	}
	return out, nil
}

// parseCaddyDial reverses BuildCaddyConfig's dial format.
//
//	"web.nvoi-myapp-prod.svc.cluster.local:80" → ("web", 80)
func parseCaddyDial(dial string) (service string, port int) {
	hostPort := strings.SplitN(dial, ":", 2)
	if len(hostPort) != 2 {
		return "", 0
	}
	host := hostPort[0]
	first := strings.SplitN(host, ".", 2)
	if len(first) < 1 || first[0] == "" {
		return "", 0
	}
	var p int
	if _, err := fmt.Sscanf(hostPort[1], "%d", &p); err != nil || p == 0 {
		return "", 0
	}
	return first[0], p
}

// PurgeCaddy deletes all Caddy workloads from kube-system (Deployment,
// Service, ConfigMap, PVC). Idempotent — NotFound on any resource is success.
// Called by the tunnel reconcile path when migrating away from Caddy.
func (c *Client) PurgeCaddy(ctx context.Context) error {
	if err := IgnoreNotFound(c.cs.AppsV1().Deployments(CaddyNamespace).Delete(ctx, CaddyName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge caddy deployment: %w", err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().Services(CaddyNamespace).Delete(ctx, CaddyName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge caddy service: %w", err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().ConfigMaps(CaddyNamespace).Delete(ctx, CaddyConfigMapName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge caddy configmap: %w", err)
	}
	if err := IgnoreNotFound(c.cs.CoreV1().PersistentVolumeClaims(CaddyNamespace).Delete(ctx, CaddyPVCName, metav1.DeleteOptions{})); err != nil {
		return fmt.Errorf("purge caddy pvc: %w", err)
	}
	return nil
}

// firstCaddyPod returns the name of the Caddy pod. Errors with a clear
// message if the pod isn't up yet so the reconciler can surface "Caddy
// not ready" instead of "no pods found".
func (c *Client) firstCaddyPod(ctx context.Context) (string, error) {
	name, err := c.FirstPod(ctx, CaddyNamespace, CaddyName)
	if err != nil {
		return "", fmt.Errorf("caddy pod not ready: %w", err)
	}
	return name, nil
}
