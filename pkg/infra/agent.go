package infra

import (
	"bytes"
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/pkg/utils"
)

const (
	agentBin     = "/usr/local/bin/nvoi"
	agentBaseDir = "/opt/nvoi"
	agentPort    = "9500"
)

// AgentDir returns the agent working directory for an app+env.
func AgentDir(app, env string) string {
	return fmt.Sprintf("%s/%s-%s", agentBaseDir, app, env)
}

// InstallAgent installs the nvoi binary, config, credentials, and systemd
// service on the master node. The agent is the deploy runtime — it holds
// credentials and executes all operations.
func InstallAgent(ctx context.Context, ssh utils.SSHClient, app, env string, config, envFile []byte) error {
	dir := AgentDir(app, env)

	// 1. Install nvoi binary via the distribution server.
	// Same installer script users run on their laptops.
	if _, err := ssh.Run(ctx, "command -v nvoi >/dev/null 2>&1 || curl -fsSL https://get.nvoi.to | sh"); err != nil {
		return fmt.Errorf("install nvoi binary: %w", err)
	}

	// 2. Create working directory.
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo mkdir -p %s && sudo chown deploy:deploy %s", dir, dir)); err != nil {
		return fmt.Errorf("create agent dir %s: %w", dir, err)
	}

	// 3. Upload config and credentials.
	if err := ssh.Upload(ctx, bytes.NewReader(config), dir+"/nvoi.yaml", 0644); err != nil {
		return fmt.Errorf("upload nvoi.yaml: %w", err)
	}
	if len(envFile) > 0 {
		if err := ssh.Upload(ctx, bytes.NewReader(envFile), dir+"/.env", 0600); err != nil {
			return fmt.Errorf("upload .env: %w", err)
		}
	}

	// 4. Install systemd service.
	unit := agentSystemdUnit(app, env, dir)
	serviceName := agentServiceName(app, env)
	if err := ssh.Upload(ctx, bytes.NewReader([]byte(unit)), "/tmp/"+serviceName, 0644); err != nil {
		return fmt.Errorf("upload systemd unit: %w", err)
	}
	if _, err := ssh.Run(ctx, fmt.Sprintf(
		"sudo mv /tmp/%s /etc/systemd/system/%s && sudo systemctl daemon-reload && sudo systemctl enable --now %s",
		serviceName, serviceName, serviceName,
	)); err != nil {
		return fmt.Errorf("enable agent service: %w", err)
	}

	return nil
}

// UpgradeAgent uploads a new nvoi binary and restarts the agent service.
func UpgradeAgent(ctx context.Context, ssh utils.SSHClient, app, env string) error {
	if _, err := ssh.Run(ctx, "curl -fsSL https://get.nvoi.to | sh"); err != nil {
		return fmt.Errorf("upgrade nvoi binary: %w", err)
	}
	serviceName := agentServiceName(app, env)
	if _, err := ssh.Run(ctx, fmt.Sprintf("sudo systemctl restart %s", serviceName)); err != nil {
		return fmt.Errorf("restart agent: %w", err)
	}
	return nil
}

// PushConfig uploads a new nvoi.yaml to the agent and signals a reload.
func PushConfig(ctx context.Context, ssh utils.SSHClient, app, env string, config []byte) error {
	dir := AgentDir(app, env)
	if err := ssh.Upload(ctx, bytes.NewReader(config), dir+"/nvoi.yaml", 0644); err != nil {
		return fmt.Errorf("upload nvoi.yaml: %w", err)
	}
	return nil
}

// PushEnv uploads a new .env to the agent's working directory.
func PushEnv(ctx context.Context, ssh utils.SSHClient, app, env string, envFile []byte) error {
	dir := AgentDir(app, env)
	if err := ssh.Upload(ctx, bytes.NewReader(envFile), dir+"/.env", 0600); err != nil {
		return fmt.Errorf("upload .env: %w", err)
	}
	return nil
}

func agentServiceName(app, env string) string {
	return fmt.Sprintf("nvoi-agent-%s-%s.service", app, env)
}

func agentSystemdUnit(app, env, dir string) string {
	return fmt.Sprintf(`[Unit]
Description=nvoi agent (%s/%s)
After=network.target k3s.service

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s agent --config %s/nvoi.yaml --addr 127.0.0.1:%s
Restart=always
RestartSec=5
User=deploy
EnvironmentFile=%s/.env

[Install]
WantedBy=multi-user.target
`, app, env, dir, agentBin, dir, agentPort, dir)
}
