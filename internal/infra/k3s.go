package infra

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
)

// InstallK3sMaster installs k3s server on the master node.
// Idempotent — skips if already installed and Ready.
func InstallK3sMaster(ctx context.Context, ip string, privateIP string, privKey []byte) error {
	ssh, err := ConnectSSH(ctx, ip+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh master: %w", err)
	}
	defer ssh.Close()

	// Already installed?
	if _, err := ssh.Run(ctx, "command -v kubectl >/dev/null 2>&1 && sudo k3s kubectl get nodes 2>/dev/null | grep -q ' Ready '"); err == nil {
		fmt.Printf("  k3s already installed\n")
		return nil
	}

	// Discover private interface
	privateIface, err := discoverPrivateInterface(ctx, ssh, privateIP)
	if err != nil {
		return fmt.Errorf("discover private interface: %w", err)
	}

	// Configure registry mirrors
	fmt.Printf("  configuring k3s registries...\n")
	if err := configureK3sRegistry(ctx, ssh, privateIP); err != nil {
		return err
	}

	// Install k3s server
	fmt.Printf("  installing k3s server...\n")
	cmd := fmt.Sprintf(
		`curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='server --disable traefik --disable servicelb --write-kubeconfig-mode 644 --node-ip %s --advertise-address %s --tls-san %s --tls-san %s --cluster-cidr %s --service-cidr %s --flannel-backend vxlan --flannel-iface %s' sh -`,
		privateIP, privateIP, privateIP, ip, core.K3sClusterCIDR, core.K3sServiceCIDR, privateIface,
	)
	if err := ssh.RunStream(ctx, cmd, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("install k3s server: %w", err)
	}

	// Setup kubeconfig for deploy user
	setupKubeconfig := fmt.Sprintf(
		`mkdir -p /home/%s/.kube && sudo cp %s /home/%s/.kube/config && sudo sed -i 's/127.0.0.1/%s/g' /home/%s/.kube/config && sudo chown -R %s:%s /home/%s/.kube && chmod 600 /home/%s/.kube/config`,
		core.DefaultUser, core.KubeconfigPath, core.DefaultUser, privateIP, core.DefaultUser, core.DefaultUser, core.DefaultUser, core.DefaultUser, core.DefaultUser,
	)
	if _, err := ssh.Run(ctx, setupKubeconfig); err != nil {
		return fmt.Errorf("setup kubeconfig: %w", err)
	}

	// Wait for cluster ready
	fmt.Printf("  waiting for k3s ready...\n")
	if err := core.Poll(ctx, 3*time.Second, 3*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config kubectl get nodes", core.DefaultUser))
		if err != nil {
			return false, nil
		}
		return strings.Contains(string(out), " Ready "), nil
	}); err != nil {
		return fmt.Errorf("k3s not ready: %w", err)
	}

	return nil
}

// EnsureRegistry starts the Docker registry container on master.
// Idempotent — skips if already running.
func EnsureRegistry(ctx context.Context, ip string, privateIP string, privKey []byte) error {
	ssh, err := ConnectSSH(ctx, ip+":22", core.DefaultUser, privKey)
	if err != nil {
		return err
	}
	defer ssh.Close()

	registryAddr := core.RegistryAddr(privateIP)

	// Already running?
	if _, err := ssh.Run(ctx, fmt.Sprintf("curl -fs http://%s/v2/ >/dev/null 2>&1", registryAddr)); err == nil {
		fmt.Printf("  registry already running at %s\n", registryAddr)
		return nil
	}

	fmt.Printf("  starting registry...\n")
	cmd := fmt.Sprintf(
		`sudo mkdir -p /var/lib/nvoi/registry && docker rm -f nvoi-registry 2>/dev/null; docker run -d --name nvoi-registry --restart always -p %d:%d -v /var/lib/nvoi/registry:/var/lib/registry -e REGISTRY_STORAGE_DELETE_ENABLED=true %s`,
		core.RegistryPort, core.RegistryPort, core.RegistryImage,
	)
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("start registry: %w", err)
	}

	// Wait for ready
	if err := core.Poll(ctx, 2*time.Second, 30*time.Second, func() (bool, error) {
		_, err := ssh.Run(ctx, fmt.Sprintf("curl -fs http://%s/v2/ >/dev/null 2>&1", registryAddr))
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("registry not ready at %s: %w", registryAddr, err)
	}

	fmt.Printf("  ✓ registry at %s\n", registryAddr)
	return nil
}

// JoinK3sWorker joins a worker to the cluster via the master.
// Idempotent — skips if k3s-agent is already active.
// workerPrivateIP comes from the provider API (server.PrivateIP).
func JoinK3sWorker(ctx context.Context, workerIP string, workerPrivateIP string, masterIP string, masterPrivateIP string, privKey []byte) error {
	// Read token from master
	masterSSH, err := ConnectSSH(ctx, masterIP+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh master for token: %w", err)
	}
	tokenBytes, err := masterSSH.Run(ctx, "sudo cat "+core.K3sTokenPath)
	masterSSH.Close()
	if err != nil {
		return fmt.Errorf("read k3s token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))

	// SSH to worker
	workerSSH, err := ConnectSSH(ctx, workerIP+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh worker: %w", err)
	}
	defer workerSSH.Close()

	// Already joined?
	if _, err := workerSSH.Run(ctx, "systemctl is-active --quiet k3s-agent"); err == nil {
		fmt.Printf("  worker already joined\n")
		return nil
	}

	// Configure registry on worker
	if err := configureK3sRegistry(ctx, workerSSH, masterPrivateIP); err != nil {
		return err
	}

	if workerPrivateIP == "" {
		workerPrivateIP = workerIP
	}

	privateIface, err := discoverPrivateInterface(ctx, workerSSH, workerPrivateIP)
	if err != nil {
		return fmt.Errorf("discover worker private interface: %w", err)
	}

	// Install k3s agent
	fmt.Printf("  installing k3s agent...\n")
	cmd := fmt.Sprintf(
		`curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s INSTALL_K3S_EXEC='agent --node-ip %s --flannel-iface %s' sh -`,
		masterPrivateIP, token, workerPrivateIP, privateIface,
	)
	if err := workerSSH.RunStream(ctx, cmd, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("install k3s agent: %w", err)
	}

	// Wait for node Ready on master
	masterSSH2, err := ConnectSSH(ctx, masterIP+":22", core.DefaultUser, privKey)
	if err != nil {
		return fmt.Errorf("ssh master to verify worker: %w", err)
	}
	defer masterSSH2.Close()

	kubeconfig := fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config", core.DefaultUser)
	fmt.Printf("  waiting for worker to be Ready...\n")
	if err := core.Poll(ctx, 3*time.Second, 3*time.Minute, func() (bool, error) {
		out, err := masterSSH2.Run(ctx, fmt.Sprintf("%s kubectl get nodes -o wide", kubeconfig))
		if err != nil {
			return false, nil
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, workerPrivateIP) && strings.Contains(line, " Ready ") {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return fmt.Errorf("worker not ready: %w", err)
	}

	return nil
}

// --- helpers ---

func configureK3sRegistry(ctx context.Context, ssh core.SSHClient, registryHost string) error {
	cmd := fmt.Sprintf(`sudo mkdir -p %s
cat <<'EOF' | sudo tee %s >/dev/null
mirrors:
  "%s:%d":
    endpoint:
      - "http://%s:%d"
EOF`, core.K3sConfigDir, core.K3sRegistriesConfig, registryHost, core.RegistryPort, registryHost, core.RegistryPort)
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("configure k3s registry: %w", err)
	}
	_, _ = ssh.Run(ctx, "sudo systemctl restart k3s 2>/dev/null || true")
	_, _ = ssh.Run(ctx, "sudo systemctl restart k3s-agent 2>/dev/null || true")
	return nil
}

func discoverPrivateInterface(ctx context.Context, ssh core.SSHClient, privateIP string) (string, error) {
	cmd := fmt.Sprintf(`ip -o -4 addr show | awk '/%s/{print $2}' | head -1`, privateIP)
	out, err := ssh.Run(ctx, cmd)
	if err != nil {
		return "", err
	}
	iface := strings.TrimSpace(string(out))
	if iface != "" {
		return iface, nil
	}
	// Fallback
	cmd = fmt.Sprintf(`ip -4 addr show | grep '%s' -B2 | grep -oP '(?<=: )[^:@]+(?=:)' | tail -1`, privateIP)
	out, err = ssh.Run(ctx, cmd)
	if err != nil {
		return "", err
	}
	iface = strings.TrimSpace(string(out))
	if iface == "" {
		return "", fmt.Errorf("no interface found for private ip %s", privateIP)
	}
	return iface, nil
}
