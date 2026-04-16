package infra

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// RunOnMaster executes a shell command on the master node.
// Bootstrap: wraps real SSH. Agent: wraps exec.Command.
type RunOnMaster = func(ctx context.Context, cmd string) ([]byte, error)

// WaitForCertificate polls until Traefik's ACME storage contains a valid cert for the domain.
// Pulls acme.json and parses it in Go — no jq, no grep, no shell string matching.
//
// Pre-checks that the Traefik deployment exists in kube-system before polling.
// If the deployment name changes across k3s versions, this fails fast with a
// diagnostic error instead of silently timing out after 10 minutes.
func WaitForCertificate(ctx context.Context, run RunOnMaster, domain string) error {
	kubeconfig := fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config", utils.DefaultUser)

	// Verify traefik deployment exists before entering the 10-minute poll loop.
	checkCmd := fmt.Sprintf("%s kubectl -n kube-system get deploy traefik --no-headers 2>/dev/null", kubeconfig)
	if out, err := run(ctx, checkCmd); err != nil || len(strings.TrimSpace(string(out))) == 0 {
		listCmd := fmt.Sprintf("%s kubectl -n kube-system get deploy -o name 2>/dev/null", kubeconfig)
		listOut, _ := run(ctx, listCmd)
		deploys := strings.TrimSpace(string(listOut))
		if deploys == "" {
			deploys = "(none)"
		}
		return fmt.Errorf("traefik deployment not found in kube-system — cannot read acme.json. deployments found: %s", deploys)
	}

	execCmd := fmt.Sprintf("%s kubectl -n kube-system exec deploy/traefik -- cat /data/acme.json 2>/dev/null", kubeconfig)

	var lastReason string
	err := utils.Poll(ctx, 3*time.Second, 10*time.Minute, func() (bool, error) {
		out, err := run(ctx, execCmd)
		if err != nil {
			lastReason = "traefik pod not reachable via exec"
			return false, nil
		}
		if len(bytes.TrimSpace(out)) == 0 {
			lastReason = "acme.json is empty — ACME challenge may not have started"
			return false, nil
		}
		if !acmeHasCert(out, domain) {
			lastReason = fmt.Sprintf("acme.json exists but has no certificate for %s yet", domain)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", lastReason, err)
	}
	return nil
}

// acmeHasCert checks if Traefik's acme.json contains a certificate for the given domain.
func acmeHasCert(data []byte, domain string) bool {
	var store map[string]struct {
		Certificates []struct {
			Domain struct {
				Main string   `json:"main"`
				SANs []string `json:"sans"`
			} `json:"domain"`
			Certificate string `json:"certificate"`
		} `json:"Certificates"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return false
	}
	for _, resolver := range store {
		for _, cert := range resolver.Certificates {
			if cert.Certificate == "" {
				continue // no actual cert data
			}
			if cert.Domain.Main == domain {
				return true
			}
			for _, san := range cert.Domain.SANs {
				if san == domain {
					return true
				}
			}
		}
	}
	return false
}

// WaitForHTTPS verifies the domain responds over HTTPS with a valid certificate.
// Rejects self-signed certs — confirms Traefik has loaded the ACME cert into its router.
// Any non-5xx response = success. Auth (401/403) is fine.
// Runs curl on the master — no dependency on client DNS propagation.
func WaitForHTTPS(ctx context.Context, run RunOnMaster, domain, healthPath string) error {
	url := fmt.Sprintf("https://%s%s", domain, healthPath)
	cmd := fmt.Sprintf("curl -s --connect-timeout 5 -o /dev/null -w '%%{http_code}' '%s'", url)
	return utils.Poll(ctx, 3*time.Second, 5*time.Minute, func() (bool, error) {
		out, err := run(ctx, cmd)
		if err != nil {
			return false, nil
		}
		code := strings.TrimSpace(strings.Trim(string(out), "'"))
		// 5xx = server error, retry. Anything else = TLS works, service responds.
		return len(code) == 3 && code[0] != '5' && code[0] != '0', nil
	})
}

// WaitSSH polls until SSH is reachable on addr (host:port).
func WaitSSH(ctx context.Context, addr string, privateKey []byte) error {
	return utils.Poll(ctx, 2*time.Second, 3*time.Minute, func() (bool, error) {
		conn, err := ConnectSSH(ctx, addr, utils.DefaultUser, privateKey)
		if err != nil {
			if errors.Is(err, ErrHostKeyChanged) {
				return false, err
			}
			if errors.Is(err, ErrAuthFailed) {
				return false, fmt.Errorf("%w for %s — server does not accept this key", ErrAuthFailed, addr)
			}
			return false, nil // transient — retry
		}
		defer conn.Close()
		_, err = conn.Run(ctx, "true")
		return err == nil, nil
	})
}

// EnsureDocker installs Docker if not present and waits until it's ready.
func EnsureDocker(ctx context.Context, ssh utils.SSHClient) error {
	// Already installed? Still ensure user is in docker group (some images
	// ship Docker pre-installed but the deploy user isn't in the group).
	if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
		_, _ = ssh.Run(ctx, "sudo usermod -aG docker "+utils.DefaultUser)
		return nil
	}

	// Wait for cloud-init to finish — networking and package state depend on it.
	// We only care that it completed, not whether it succeeded.
	// cloud-init may not exist on some images — that's fine.
	ssh.Run(ctx, "cloud-init status --wait >/dev/null 2>&1")

	install := func() error {
		for _, cmd := range []string{
			"sudo timeout 120 bash -lc 'while fuser /var/lib/dpkg/lock >/dev/null 2>&1 || fuser /var/lib/dpkg/lock-frontend >/dev/null 2>&1; do sleep 2; done'",
			"sudo dpkg --configure -a >/dev/null 2>&1 || true",
			"curl -fsSL https://get.docker.com | sudo sh",
			"sudo usermod -aG docker " + utils.DefaultUser,
			"sudo systemctl enable docker",
			"sudo systemctl start docker",
		} {
			if _, err := ssh.Run(ctx, cmd); err != nil {
				return fmt.Errorf("docker: %s: %w", strings.Split(cmd, " ")[0], err)
			}
		}
		return nil
	}

	if err := utils.Poll(ctx, 5*time.Second, 3*time.Minute, func() (bool, error) {
		if _, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1"); err == nil {
			return true, nil
		}
		if err := install(); err != nil {
			return false, nil
		}
		_, err := ssh.Run(ctx, "sudo docker info >/dev/null 2>&1")
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("docker install did not converge: %w", err)
	}

	return nil
}
