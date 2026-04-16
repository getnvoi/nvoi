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
	"github.com/getnvoi/nvoi/internal/render"
	app "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
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
// Returns the payload from the last data event (if any) and the last error.
func (b *agentBackend) streamCommand(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	defer resp.Body.Close()

	// Stream JSONL events and replay through the output interface.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	var lastErr error
	var dataPayload json.RawMessage
	for scanner.Scan() {
		ev, err := app.ParseEvent(scanner.Text())
		if err != nil {
			continue
		}
		if ev.Type == app.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		if ev.Type == app.EventData {
			dataPayload = ev.Payload
			continue
		}
		app.ReplayEvent(ev, b.out)
	}
	return dataPayload, lastErr
}

// ── Backend interface implementation ────────────────────────────────────────

func (b *agentBackend) Deploy(ctx context.Context) error {
	if err := b.pushConfig(ctx); err != nil {
		return err
	}
	_, err := b.streamCommand(ctx, "POST", "/deploy", nil)
	return err
}

func (b *agentBackend) Teardown(ctx context.Context, deleteVolumes, deleteStorage bool) error {
	if err := b.pushConfig(ctx); err != nil {
		return err
	}
	_, err := b.streamCommand(ctx, "POST", "/teardown", map[string]any{
		"delete_volumes": deleteVolumes,
		"delete_storage": deleteStorage,
	})
	return err
}

func (b *agentBackend) Describe(ctx context.Context, jsonOutput bool) error {
	data, err := b.streamCommand(ctx, "GET", "/describe", nil)
	if err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}
	var res app.DescribeResult
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("decode describe: %w", err)
	}
	render.RenderDescribe(&res)
	return nil
}

func (b *agentBackend) Resources(ctx context.Context, jsonOutput bool) error {
	data, err := b.streamCommand(ctx, "GET", "/resources", nil)
	if err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}
	var groups []provider.ResourceGroup
	if err := json.Unmarshal(data, &groups); err != nil {
		return fmt.Errorf("decode resources: %w", err)
	}
	render.RenderResources(groups)
	return nil
}

func (b *agentBackend) Logs(ctx context.Context, opts LogsOpts) error {
	path := fmt.Sprintf("/logs/%s?follow=%t&tail=%d&since=%s&previous=%t&timestamps=%t",
		opts.Service, opts.Follow, opts.Tail, opts.Since, opts.Previous, opts.Timestamps)
	_, err := b.streamCommand(ctx, "GET", path, nil)
	return err
}

func (b *agentBackend) Exec(ctx context.Context, service string, command []string) error {
	_, err := b.streamCommand(ctx, "POST", "/exec/"+service, map[string]any{
		"command": command,
	})
	return err
}

func (b *agentBackend) SSH(ctx context.Context, command []string) error {
	_, err := b.streamCommand(ctx, "POST", "/ssh", map[string]any{
		"command": command,
	})
	return err
}

func (b *agentBackend) CronRun(ctx context.Context, name string) error {
	_, err := b.streamCommand(ctx, "POST", "/cron/"+name+"/run", nil)
	return err
}

func (b *agentBackend) DatabaseBackupList(ctx context.Context, dbName string) error {
	data, err := b.streamCommand(ctx, "GET", "/db/"+dbName+"/backups", nil)
	if err != nil {
		return err
	}
	b.out.Command("database", "backup list", dbName)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(json.RawMessage(data))
}

func (b *agentBackend) DatabaseBackupDownload(ctx context.Context, dbName, key, outFile string) error {
	// Backup download is raw binary — not JSONL. Direct HTTP.
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
	data, err := b.streamCommand(ctx, "POST", "/db/"+dbName+"/sql", map[string]any{
		"engine": engine,
		"query":  query,
	})
	if err != nil {
		return err
	}
	// SQL output is a string wrapped in a data event payload.
	var output string
	json.Unmarshal(data, &output)
	fmt.Print(output)
	return nil
}

// CLI-side credential resolution delegates to internal/agent for the shared
// logic. The CLI only needs compute creds + SSH key to reach the master.
// resolveAgentSSHKey lives in cmd/cli/agent.go (the os.Getenv boundary).
