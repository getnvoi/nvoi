package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/agent"
	"github.com/getnvoi/nvoi/internal/config"
	app "github.com/getnvoi/nvoi/pkg/core"
)

const agentPort = "9500"

// ErrNoMaster indicates the agent is unreachable because no master server
// exists or credentials are insufficient to reach it. Bootstrap is needed.
var ErrNoMaster = fmt.Errorf("no master")

// agentBackend talks to the agent over an SSH tunnel to localhost:{agentPort}.
// The agent is the deploy runtime — it holds credentials and executes everything.
type agentBackend struct {
	client     *http.Client // HTTP client pointed at the SSH tunnel
	baseURL    string
	out        app.Output
	configPath string // local nvoi.yaml — pushed to agent before deploy
}

// connectToAgent resolves the master IP, SSHes in, port-forwards to the
// agent's localhost listener, and returns an agentBackend. The SSH tunnel
// stays open for the lifetime of the returned cleanup function.
func connectToAgent(ctx context.Context, out app.Output, cfg *config.AppConfig, configPath string) (*agentBackend, func(), error) {
	// Resolve master IP from provider API.
	source := agent.CredentialSource(ctx, cfg)
	computeCreds, err := agent.ResolveProviderCreds(source, "compute", cfg.Providers.Compute)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNoMaster, err)
	}
	sshKey, err := resolveAgentSSHKey()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNoMaster, err)
	}

	cluster := app.Cluster{
		AppName:     cfg.App,
		Env:         cfg.Env,
		Provider:    cfg.Providers.Compute,
		Credentials: computeCreds,
		SSHKey:      sshKey,
		Output:      out,
	}

	master, _, _, err := cluster.Master(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNoMaster, err)
	}

	// SSH into master and port-forward to agent.
	ssh, err := cluster.Connect(ctx, master.IPv4+":22")
	if err != nil {
		return nil, nil, fmt.Errorf("SSH to master %s: %w", master.IPv4, err)
	}

	// Create a local listener and forward connections through SSH to agent.
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ssh.Close()
		return nil, nil, fmt.Errorf("local listener: %w", err)
	}
	localAddr := localListener.Addr().String()

	// Forward connections from the local listener to the agent via SSH tunnel.
	go func() {
		for {
			local, err := localListener.Accept()
			if err != nil {
				return // listener closed
			}
			remote, err := ssh.DialTCP(ctx, "127.0.0.1:"+agentPort)
			if err != nil {
				local.Close()
				continue
			}
			go func() {
				defer local.Close()
				defer remote.Close()
				done := make(chan struct{}, 2)
				go func() { io.Copy(remote, local); done <- struct{}{} }()
				go func() { io.Copy(local, remote); done <- struct{}{} }()
				<-done
			}()
		}
	}()

	cleanup := func() {
		localListener.Close()
		ssh.Close()
	}

	return &agentBackend{
		client:     &http.Client{},
		baseURL:    "http://" + localAddr,
		out:        out,
		configPath: configPath,
	}, cleanup, nil
}

// pushConfig uploads the local nvoi.yaml to the agent before commands.
func (b *agentBackend) pushConfig(ctx context.Context) error {
	data, err := os.ReadFile(b.configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+"/config", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("push config to agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent rejected config: %s", string(body))
	}
	return nil
}

// streamCommand sends a request to the agent and streams JSONL events to the output.
func (b *agentBackend) streamCommand(ctx context.Context, method, path string, body any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	defer resp.Body.Close()

	// Stream JSONL events and replay through the output interface.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var lastErr error
	for scanner.Scan() {
		ev, err := app.ParseEvent(scanner.Text())
		if err != nil {
			continue
		}
		if ev.Type == app.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		app.ReplayEvent(ev, b.out)
	}
	return lastErr
}

// jsonCommand sends a request and returns the JSON response body.
func (b *agentBackend) jsonCommand(ctx context.Context, method, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent %d: %s", resp.StatusCode, string(body))
	}
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

// ── Backend interface implementation ────────────────────────────────────────

func (b *agentBackend) Deploy(ctx context.Context) error {
	if err := b.pushConfig(ctx); err != nil {
		return err
	}
	return b.streamCommand(ctx, "POST", "/deploy", nil)
}

func (b *agentBackend) Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error {
	if err := b.pushConfig(ctx); err != nil {
		return err
	}
	return b.streamCommand(ctx, "POST", "/teardown", map[string]any{
		"delete_volumes": deleteVolumes,
		"delete_storage": deleteStorage,
	})
}

func (b *agentBackend) Describe(ctx context.Context, jsonOutput bool) error {
	if jsonOutput {
		var raw any
		if err := b.jsonCommand(ctx, "GET", "/describe", &raw); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(raw)
	}
	// Describe streams JSONL for TUI rendering.
	return b.streamCommand(ctx, "GET", "/describe", nil)
}

func (b *agentBackend) Resources(ctx context.Context, jsonOutput bool) error {
	if jsonOutput {
		var raw any
		if err := b.jsonCommand(ctx, "GET", "/resources", &raw); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(raw)
	}
	return b.streamCommand(ctx, "GET", "/resources", nil)
}

func (b *agentBackend) Logs(ctx context.Context, opts LogsOpts) error {
	path := fmt.Sprintf("/logs/%s?follow=%t&tail=%d&since=%s&previous=%t&timestamps=%t",
		opts.Service, opts.Follow, opts.Tail, opts.Since, opts.Previous, opts.Timestamps)
	return b.streamCommand(ctx, "GET", path, nil)
}

func (b *agentBackend) Exec(ctx context.Context, service string, command []string) error {
	return b.streamCommand(ctx, "POST", "/exec/"+service, map[string]any{
		"command": command,
	})
}

func (b *agentBackend) SSH(ctx context.Context, command []string) error {
	return b.streamCommand(ctx, "POST", "/ssh", map[string]any{
		"command": command,
	})
}

func (b *agentBackend) CronRun(ctx context.Context, name string) error {
	return b.streamCommand(ctx, "POST", "/cron/"+name+"/run", nil)
}

func (b *agentBackend) DatabaseBackupList(ctx context.Context, dbName string) error {
	path := "/db/" + dbName + "/backups"
	var entries []any
	if err := b.jsonCommand(ctx, "GET", path, &entries); err != nil {
		return err
	}
	b.out.Command("database", "backup list", dbName)
	if len(entries) == 0 {
		b.out.Info("no backups found")
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

func (b *agentBackend) DatabaseBackupDownload(ctx context.Context, dbName, key, outFile string) error {
	path := "/db/" + dbName + "/backups/" + key
	req, err := http.NewRequestWithContext(ctx, "GET", b.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	defer resp.Body.Close()

	b.out.Command("database", "backup download", key)
	var w io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		return err
	}
	if outFile != "" {
		b.out.Success(fmt.Sprintf("downloaded %s (%d bytes)", outFile, n))
	}
	return nil
}

func (b *agentBackend) DatabaseSQL(ctx context.Context, dbName, engine, query string) error {
	path := "/db/" + dbName + "/sql"
	req, err := http.NewRequestWithContext(ctx, "POST", b.baseURL+path, strings.NewReader(
		fmt.Sprintf(`{"engine":%q,"query":%q}`, engine, query),
	))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Print(string(body))
	return nil
}

// CLI-side credential resolution delegates to internal/agent for the shared
// logic. The CLI only needs compute creds + SSH key to reach the master.
// resolveAgentSSHKey lives in cmd/cli/agent.go (the os.Getenv boundary).
