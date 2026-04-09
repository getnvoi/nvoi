package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/commands"
	"github.com/getnvoi/nvoi/internal/render"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
)

// providerKinds is the single source of truth for the four provider kinds.
// Used by repos use flags, provider commands, and credential resolution.
var providerKinds = []string{"compute", "dns", "storage", "build"}

// CloudBackend implements commands.Backend by calling the nvoi API.
// Every mutation command calls /run — one API call, one pkg/core/ function,
// JSONL output streamed back through the TUI renderer.
type CloudBackend struct {
	client *APIClient
	wsID   string
	repoID string
}

// buildCloudBackend loads auth and returns an authenticated CloudBackend.
func buildCloudBackend() (*CloudBackend, error) {
	client, cfg, err := authedClient()
	if err != nil {
		return nil, err
	}
	wsID, repoID, err := requireRepo(cfg)
	if err != nil {
		return nil, err
	}
	return &CloudBackend{client: client, wsID: wsID, repoID: repoID}, nil
}

// repoPath builds the scoped API path for a resource under the active repo.
func (c *CloudBackend) repoPath(suffix string) string {
	return "/workspaces/" + c.wsID + "/repos/" + c.repoID + suffix
}

// esc escapes a user-controlled value for safe use in URL paths.
func esc(s string) string {
	return url.PathEscape(s)
}

// ── run — the single dispatch point ─────────────────────────────────────────

// run POSTs to /run with a command kind + name + params, streams JSONL through the TUI.
func (c *CloudBackend) run(ctx context.Context, kind, name string, params map[string]any) error {
	body := map[string]any{
		"kind": kind,
		"name": name,
	}
	if params != nil {
		body["params"] = params
	}

	resp, err := c.client.doRawWithBody("POST", c.repoPath("/run"), body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out := render.Resolve(false, false)
	var lastErr error
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		ev, err := pkgcore.ParseEvent(line)
		if err != nil {
			continue
		}
		if ev.Type == pkgcore.EventError {
			lastErr = fmt.Errorf("%s", ev.Message)
		}
		pkgcore.ReplayEvent(ev, out)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return lastErr
}

// ── Backend implementation — mutation commands ──────────────────────────────

func (c *CloudBackend) InstanceSet(ctx context.Context, name, serverType, region, role string) error {
	return c.run(ctx, "instance.set", name, map[string]any{
		"type": serverType, "region": region, "role": role,
	})
}

func (c *CloudBackend) InstanceDelete(ctx context.Context, name string) error {
	return c.run(ctx, "instance.delete", name, nil)
}

func (c *CloudBackend) FirewallSet(ctx context.Context, args []string) error {
	params := map[string]any{}
	var rules map[string][]string
	for _, arg := range args {
		if strings.Contains(arg, ":") {
			port, cidrs, _ := strings.Cut(arg, ":")
			if rules == nil {
				rules = map[string][]string{}
			}
			rules[port] = strings.Split(cidrs, ",")
		} else {
			params["preset"] = arg
		}
	}
	if rules != nil {
		params["rules"] = rules
	}
	return c.run(ctx, "firewall.set", "firewall", params)
}

func (c *CloudBackend) VolumeSet(ctx context.Context, name string, size int, server string) error {
	return c.run(ctx, "volume.set", name, map[string]any{
		"size": size, "server": server,
	})
}

func (c *CloudBackend) VolumeDelete(ctx context.Context, name string) error {
	return c.run(ctx, "volume.delete", name, nil)
}

func (c *CloudBackend) StorageSet(ctx context.Context, name, bucket string, cors bool, expireDays int) error {
	params := map[string]any{}
	if cors {
		params["cors"] = true
	}
	if expireDays > 0 {
		params["expire_days"] = expireDays
	}
	if bucket != "" {
		params["bucket"] = bucket
	}
	return c.run(ctx, "storage.set", name, params)
}

func (c *CloudBackend) StorageDelete(ctx context.Context, name string) error {
	return c.run(ctx, "storage.delete", name, nil)
}

func (c *CloudBackend) ServiceSet(ctx context.Context, name string, opts commands.ServiceOpts) error {
	params := map[string]any{}
	if opts.Image != "" {
		params["image"] = opts.Image
	}
	if opts.Port > 0 {
		params["port"] = opts.Port
	}
	if opts.Replicas > 0 {
		params["replicas"] = opts.Replicas
	}
	if opts.Command != "" {
		params["command"] = opts.Command
	}
	if opts.Health != "" {
		params["health"] = opts.Health
	}
	if opts.Server != "" {
		params["server"] = opts.Server
	}
	if len(opts.Env) > 0 {
		params["env"] = opts.Env
	}
	if len(opts.Secrets) > 0 {
		params["secrets"] = opts.Secrets
	}
	if len(opts.Storage) > 0 {
		params["storage"] = opts.Storage
	}
	if len(opts.Volumes) > 0 {
		params["volumes"] = opts.Volumes
	}
	return c.run(ctx, "service.set", name, params)
}

func (c *CloudBackend) ServiceDelete(ctx context.Context, name string) error {
	return c.run(ctx, "service.delete", name, nil)
}

func (c *CloudBackend) CronSet(ctx context.Context, name string, opts commands.CronOpts) error {
	params := map[string]any{
		"schedule": opts.Schedule,
	}
	if opts.Image != "" {
		params["image"] = opts.Image
	}
	if opts.Command != "" {
		params["command"] = opts.Command
	}
	if opts.Server != "" {
		params["server"] = opts.Server
	}
	if len(opts.Env) > 0 {
		params["env"] = opts.Env
	}
	if len(opts.Secrets) > 0 {
		params["secrets"] = opts.Secrets
	}
	if len(opts.Storage) > 0 {
		params["storage"] = opts.Storage
	}
	if len(opts.Volumes) > 0 {
		params["volumes"] = opts.Volumes
	}
	return c.run(ctx, "cron.set", name, params)
}

func (c *CloudBackend) CronDelete(ctx context.Context, name string) error {
	return c.run(ctx, "cron.delete", name, nil)
}

func (c *CloudBackend) SecretSet(ctx context.Context, key, value string) error {
	return c.run(ctx, "secret.set", key, map[string]any{"value": value})
}

func (c *CloudBackend) SecretDelete(ctx context.Context, key string) error {
	return c.run(ctx, "secret.delete", key, nil)
}

func (c *CloudBackend) Build(ctx context.Context, opts commands.BuildOpts) error {
	for _, target := range opts.Targets {
		name, source, ok := strings.Cut(target, ":")
		if !ok {
			return fmt.Errorf("invalid build target %q — expected name:source", target)
		}
		if err := c.run(ctx, "build", name, map[string]any{"source": source}); err != nil {
			return err
		}
	}
	return nil
}

func (c *CloudBackend) DNSSet(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		if err := c.run(ctx, "dns.set", route.Service, map[string]any{"domains": route.Domains}); err != nil {
			return err
		}
	}
	return nil
}

func (c *CloudBackend) DNSDelete(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		if err := c.run(ctx, "dns.delete", route.Service, map[string]any{"domains": route.Domains}); err != nil {
			return err
		}
	}
	return nil
}

func (c *CloudBackend) IngressSet(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		if err := c.run(ctx, "ingress.set", route.Service, map[string]any{
			"service": route.Service, "domains": route.Domains,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *CloudBackend) IngressDelete(ctx context.Context, routes []commands.RouteArg) error {
	for _, route := range routes {
		if err := c.run(ctx, "ingress.delete", route.Service, map[string]any{
			"service": route.Service, "domains": route.Domains,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ── Backend implementation — read-only queries ──────────────────────────────

func (c *CloudBackend) Describe(ctx context.Context, jsonOutput bool) error {
	var res pkgcore.DescribeResult
	if err := c.client.Do("GET", c.repoPath("/describe"), nil, &res); err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	render.RenderDescribe(&res)
	return nil
}

func (c *CloudBackend) Resources(ctx context.Context, jsonOutput bool) error {
	var groups []provider.ResourceGroup
	if err := c.client.Do("GET", c.repoPath("/resources"), nil, &groups); err != nil {
		return err
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(groups)
	}
	render.RenderResources(groups)
	return nil
}

func (c *CloudBackend) InstanceList(ctx context.Context) error {
	var servers []struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		IPv4      string `json:"ipv4"`
		PrivateIP string `json:"private_ip"`
	}
	if err := c.client.Do("GET", c.repoPath("/instances"), nil, &servers); err != nil {
		return err
	}
	t := render.NewTable("NAME", "STATUS", "IPv4", "PRIVATE IP")
	for _, s := range servers {
		t.Row(s.Name, s.Status, s.IPv4, s.PrivateIP)
	}
	t.Print()
	return nil
}

func (c *CloudBackend) FirewallList(ctx context.Context) error {
	return fmt.Errorf("firewall list not yet available in cloud mode")
}

func (c *CloudBackend) VolumeList(ctx context.Context) error {
	var volumes []struct {
		Name       string `json:"name"`
		Size       int    `json:"size"`
		ServerName string `json:"server_name"`
		DevicePath string `json:"device_path"`
	}
	if err := c.client.Do("GET", c.repoPath("/volumes"), nil, &volumes); err != nil {
		return err
	}
	t := render.NewTable("NAME", "SIZE", "SERVER", "DEVICE")
	for _, v := range volumes {
		t.Row(v.Name, fmt.Sprintf("%dGB", v.Size), v.ServerName, v.DevicePath)
	}
	t.Print()
	return nil
}

func (c *CloudBackend) StorageEmpty(ctx context.Context, name string) error {
	return c.client.Do("POST", c.repoPath("/storage/"+esc(name)+"/empty"), nil, nil)
}

func (c *CloudBackend) StorageList(ctx context.Context) error {
	var items []struct {
		Name   string `json:"name"`
		Bucket string `json:"bucket"`
	}
	if err := c.client.Do("GET", c.repoPath("/storage"), nil, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("no storage configured")
		return nil
	}
	for _, item := range items {
		fmt.Printf("%-20s %s\n", item.Name, item.Bucket)
	}
	return nil
}

func (c *CloudBackend) SecretList(ctx context.Context) error {
	var keys []string
	if err := c.client.Do("GET", c.repoPath("/secrets"), nil, &keys); err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Println("no secrets")
		return nil
	}
	t := render.NewTable("KEY")
	for _, k := range keys {
		t.Row(k)
	}
	t.Print()
	return nil
}

func (c *CloudBackend) SecretReveal(_ context.Context, key string) (string, error) {
	return "", fmt.Errorf("secret reveal is not available in cloud mode — secrets are write-only")
}

func (c *CloudBackend) DNSList(ctx context.Context) error {
	var records []struct {
		Type   string `json:"type"`
		Domain string `json:"domain"`
		IP     string `json:"ip"`
	}
	if err := c.client.Do("GET", c.repoPath("/dns"), nil, &records); err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("no records")
		return nil
	}
	for _, r := range records {
		fmt.Printf("%-4s %-40s %s\n", r.Type, r.Domain, r.IP)
	}
	return nil
}

func (c *CloudBackend) BuildList(ctx context.Context) error {
	var images []struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := c.client.Do("GET", c.repoPath("/builds"), nil, &images); err != nil {
		return err
	}
	if len(images) == 0 {
		fmt.Println("no images in registry")
		return nil
	}
	t := render.NewTable("IMAGE", "TAGS")
	for _, img := range images {
		t.Row(img.Name, strings.Join(img.Tags, ", "))
	}
	t.Print()
	return nil
}

func (c *CloudBackend) BuildLatest(ctx context.Context, name string) (string, error) {
	var resp struct {
		Ref string `json:"ref"`
	}
	if err := c.client.Do("GET", c.repoPath("/builds/"+esc(name)+"/latest"), nil, &resp); err != nil {
		return "", err
	}
	return resp.Ref, nil
}

func (c *CloudBackend) BuildPrune(ctx context.Context, name string, keep int) error {
	return c.client.Do("POST", c.repoPath("/builds/"+esc(name)+"/prune"), map[string]any{"keep": keep}, nil)
}

func (c *CloudBackend) SSH(ctx context.Context, command []string) error {
	resp, err := c.client.doRawWithBody("POST", c.repoPath("/ssh"), map[string]any{"command": command})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}
	return scanner.Err()
}

func (c *CloudBackend) Exec(ctx context.Context, service string, command []string) error {
	resp, err := c.client.doRawWithBody("POST", c.repoPath("/services/"+esc(service)+"/exec"), map[string]any{
		"command": command,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func (c *CloudBackend) Logs(ctx context.Context, service string, opts commands.LogsOpts) error {
	path := fmt.Sprintf("/services/%s/logs?tail=%d&since=%s", service, opts.Tail, opts.Since)
	if opts.Follow {
		path += "&follow=true"
	}
	if opts.Previous {
		path += "&previous=true"
	}
	if opts.Timestamps {
		path += "&timestamps=true"
	}
	resp, err := c.client.doRaw("GET", c.repoPath(path))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}
