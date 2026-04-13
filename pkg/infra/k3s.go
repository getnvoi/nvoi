package infra

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getnvoi/nvoi/pkg/utils"
)

// Node identifies a server by its public and private IP addresses.
// Groups the ip+privateIP pairs that are passed together everywhere.
type Node struct {
	PublicIP  string
	PrivateIP string
}

// InstallK3sMaster installs k3s server on the master node.
// Idempotent — skips if already installed and Ready.
func InstallK3sMaster(ctx context.Context, ssh utils.SSHClient, node Node, w io.Writer) error {
	// Already installed?
	// Check if k3s is installed and at least one node is truly Ready.
	// Uses jsonpath to query the Ready condition — avoids "NotReady" matching "Ready".
	out2, err := ssh.Run(ctx, `command -v kubectl >/dev/null 2>&1 && sudo k3s kubectl get nodes -o jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null`)
	if err == nil && strings.Contains(string(out2), "True") {
		fmt.Fprintln(w, "k3s already installed")
		return nil
	}

	// Discover private interface
	privateIface, err := discoverPrivateInterface(ctx, ssh, node.PrivateIP)
	if err != nil {
		return fmt.Errorf("discover private interface: %w", err)
	}

	// Configure registry mirrors
	fmt.Fprintln(w, "configuring k3s registries...")
	if err := configureK3sRegistry(ctx, ssh, node.PrivateIP); err != nil {
		return err
	}

	// Install k3s server
	fmt.Fprintln(w, "installing k3s server...")
	cmd := fmt.Sprintf(
		`curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='server --write-kubeconfig-mode 644 --node-ip %s --advertise-address %s --tls-san %s --tls-san %s --cluster-cidr %s --service-cidr %s --flannel-backend vxlan --flannel-iface %s' sh -`,
		node.PrivateIP, node.PrivateIP, node.PrivateIP, node.PublicIP, utils.K3sClusterCIDR, utils.K3sServiceCIDR, privateIface,
	)
	if err := ssh.RunStream(ctx, cmd, w, w); err != nil {
		return fmt.Errorf("install k3s server: %w", err)
	}

	// Setup kubeconfig for deploy user
	setupKubeconfig := fmt.Sprintf(
		`mkdir -p /home/%s/.kube && sudo cp %s /home/%s/.kube/config && sudo sed -i 's/127.0.0.1/%s/g' /home/%s/.kube/config && sudo chown -R %s:%s /home/%s/.kube && chmod 600 /home/%s/.kube/config`,
		utils.DefaultUser, utils.KubeconfigPath, utils.DefaultUser, node.PrivateIP, utils.DefaultUser, utils.DefaultUser, utils.DefaultUser, utils.DefaultUser, utils.DefaultUser,
	)
	if _, err := ssh.Run(ctx, setupKubeconfig); err != nil {
		return fmt.Errorf("setup kubeconfig: %w", err)
	}

	// Wait for cluster ready
	fmt.Fprintln(w, "waiting for k3s ready...")
	if err := utils.Poll(ctx, 3*time.Second, 3*time.Minute, func() (bool, error) {
		out, err := ssh.Run(ctx, fmt.Sprintf(
			`KUBECONFIG=/home/%s/.kube/config kubectl get nodes -o jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}'`,
			utils.DefaultUser))
		if err != nil {
			return false, nil
		}
		return strings.Contains(string(out), "True"), nil
	}); err != nil {
		return fmt.Errorf("k3s not ready: %w", err)
	}

	return nil
}

// EnsureRegistry starts the Docker registry container on master.
// Idempotent — skips if already running.
func EnsureRegistry(ctx context.Context, ssh utils.SSHClient, node Node, w io.Writer) error {
	registryAddr := utils.RegistryAddr(node.PrivateIP)

	// Already running?
	if _, err := ssh.Run(ctx, fmt.Sprintf("curl -fs http://%s/v2/ >/dev/null 2>&1", registryAddr)); err == nil {
		fmt.Fprintf(w, "registry already running at %s\n", registryAddr)
		return nil
	}

	fmt.Fprintln(w, "starting registry...")
	ssh.Run(ctx, "sudo mkdir -p /var/lib/nvoi/registry")
	ssh.Run(ctx, "docker rm -f nvoi-registry 2>/dev/null || true")

	runCmd := fmt.Sprintf(
		`docker run -d --name nvoi-registry --restart always -p %d:%d -v /var/lib/nvoi/registry:/var/lib/registry -e REGISTRY_STORAGE_DELETE_ENABLED=true %s`,
		utils.RegistryPort, utils.RegistryPort, utils.RegistryImage,
	)
	if _, err := ssh.Run(ctx, runCmd); err != nil {
		return fmt.Errorf("start registry: %w", err)
	}

	// Wait for ready
	if err := utils.Poll(ctx, 2*time.Second, 30*time.Second, func() (bool, error) {
		_, err := ssh.Run(ctx, fmt.Sprintf("curl -fs http://%s/v2/ >/dev/null 2>&1", registryAddr))
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("registry not ready at %s: %w", registryAddr, err)
	}

	fmt.Fprintf(w, "registry at %s\n", registryAddr)
	return nil
}

// JoinK3sWorker joins a worker to the cluster via the master.
// Idempotent — skips if k3s-agent is already active.
// masterSSH reads the join token and verifies the node. workerSSH installs the agent.
func JoinK3sWorker(ctx context.Context, masterSSH, workerSSH utils.SSHClient, worker, master Node, w io.Writer) error {
	// Read token from master
	tokenBytes, err := masterSSH.Run(ctx, "sudo cat "+utils.K3sTokenPath)
	if err != nil {
		return fmt.Errorf("read k3s token: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))

	// Already joined?
	if _, err := workerSSH.Run(ctx, "systemctl is-active --quiet k3s-agent"); err == nil {
		fmt.Fprintln(w, "worker already joined")
		return nil
	}

	// Configure registry on worker
	if err := configureK3sRegistry(ctx, workerSSH, master.PrivateIP); err != nil {
		return err
	}

	workerPrivateIP := worker.PrivateIP
	if workerPrivateIP == "" {
		workerPrivateIP = worker.PublicIP
	}

	privateIface, err := discoverPrivateInterface(ctx, workerSSH, workerPrivateIP)
	if err != nil {
		return fmt.Errorf("discover worker private interface: %w", err)
	}

	// Install k3s agent
	fmt.Fprintln(w, "installing k3s agent...")
	cmd := fmt.Sprintf(
		`curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s INSTALL_K3S_EXEC='agent --node-ip %s --flannel-iface %s' sh -`,
		master.PrivateIP, token, workerPrivateIP, privateIface,
	)
	if err := workerSSH.RunStream(ctx, cmd, w, w); err != nil {
		return fmt.Errorf("install k3s agent: %w", err)
	}

	// Wait for worker node Ready on master
	kubeconfig := fmt.Sprintf("KUBECONFIG=/home/%s/.kube/config", utils.DefaultUser)
	fmt.Fprintln(w, "waiting for worker to be Ready...")
	if err := utils.Poll(ctx, 3*time.Second, 3*time.Minute, func() (bool, error) {
		// Query all nodes' Ready conditions and internal IPs, match by IP.
		out, err := masterSSH.Run(ctx, fmt.Sprintf(
			`%s kubectl get nodes -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status},{.status.addresses[?(@.type=="InternalIP")].address}{"\n"}{end}'`,
			kubeconfig))
		if err != nil {
			return false, nil
		}
		for _, line := range strings.Split(string(out), "\n") {
			parts := strings.SplitN(strings.Trim(line, "'"), ",", 2)
			if len(parts) == 2 && parts[0] == "True" && parts[1] == workerPrivateIP {
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

func configureK3sRegistry(ctx context.Context, ssh utils.SSHClient, registryHost string) error {
	cmd := fmt.Sprintf(`sudo mkdir -p %s
cat <<'EOF' | sudo tee %s >/dev/null
mirrors:
  "%s:%d":
    endpoint:
      - "http://%s:%d"
EOF`, utils.K3sConfigDir, utils.K3sRegistriesConfig, registryHost, utils.RegistryPort, registryHost, utils.RegistryPort)
	if _, err := ssh.Run(ctx, cmd); err != nil {
		return fmt.Errorf("configure k3s registry: %w", err)
	}

	// Restart whichever k3s service is active (server or agent), then wait for ready.
	// On fresh install neither exists yet — skip restart, the install will pick up the config.
	for _, svc := range []string{"k3s", "k3s-agent"} {
		if _, err := ssh.Run(ctx, fmt.Sprintf("sudo systemctl is-active --quiet %s 2>/dev/null", svc)); err != nil {
			continue // service not active — skip
		}
		if _, err := ssh.Run(ctx, fmt.Sprintf("sudo systemctl restart %s", svc)); err != nil {
			return fmt.Errorf("restart %s after registry config: %w", svc, err)
		}
		// Wait for k3s to be ready after restart.
		if err := utils.Poll(ctx, 2*time.Second, 60*time.Second, func() (bool, error) {
			out, err := ssh.Run(ctx, `sudo k3s kubectl get nodes -o jsonpath='{.items[*].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null`)
			if err != nil {
				return false, nil
			}
			return strings.Contains(string(out), "True"), nil
		}); err != nil {
			return fmt.Errorf("%s not ready after restart: %w", svc, err)
		}
	}
	return nil
}

func discoverPrivateInterface(ctx context.Context, ssh utils.SSHClient, privateIP string) (string, error) {
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
