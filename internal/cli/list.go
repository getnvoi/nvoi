package cli

import (
	"context"
	"fmt"

	"github.com/getnvoi/nvoi/internal/render"
)

// CloudBackend list methods. Each calls a read-only API endpoint and renders
// the result. Called by the shared command tree (internal/commands/*) when the
// user runs: nvoi instance list, nvoi volume list, nvoi dns list, etc.
//
// API endpoints: GET /instances, /volumes, /storage, /secrets, /dns
// See internal/api/handlers/query.go for the server side.

// InstanceList calls GET /instances → table of servers.
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

// FirewallList — no API endpoint yet.
func (c *CloudBackend) FirewallList(ctx context.Context) error {
	return fmt.Errorf("firewall list not yet available in cloud mode")
}

// VolumeList calls GET /volumes → table of block storage volumes.
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

// StorageEmpty calls POST /storage/:name/empty → deletes all objects in a bucket.
func (c *CloudBackend) StorageEmpty(ctx context.Context, name string) error {
	return c.client.Do("POST", c.repoPath("/storage/"+esc(name)+"/empty"), nil, nil)
}

// StorageList calls GET /storage → list of bucket names.
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

// SecretList calls GET /secrets → table of secret key names.
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

// SecretReveal — not available in cloud mode. Secrets are write-only from the CLI.
func (c *CloudBackend) SecretReveal(_ context.Context, key string) (string, error) {
	return "", fmt.Errorf("secret reveal is not available in cloud mode — secrets are write-only")
}

// DNSList calls GET /dns → list of A records.
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
