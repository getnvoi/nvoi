// Package cli is the cloud CLI — authenticates via API, mutates config, and delegates execution to the server.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/commands"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

func (c *CloudBackend) AgentList(ctx context.Context) error {
	var services []pkgcore.ManagedService
	if err := c.client.Do("GET", c.repoPath("/agent"), nil, &services); err != nil {
		return err
	}
	if len(services) == 0 {
		fmt.Println("no managed agents found")
		return nil
	}
	for _, svc := range services {
		children := strings.Join(svc.Children, ", ")
		fmt.Printf("%s  type=%s  %s  %s  children=[%s]\n", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children)
	}
	return nil
}

func (c *CloudBackend) AgentExec(ctx context.Context, name, kind string, command []string) error {
	resp, err := c.client.doRawWithBody("POST", c.repoPath("/agent/"+esc(name)+"/exec"), map[string]any{
		"command": command,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func (c *CloudBackend) AgentLogs(ctx context.Context, name, kind string, opts commands.LogsOpts) error {
	path := fmt.Sprintf("/agent/%s/logs?tail=%d&since=%s", name, opts.Tail, opts.Since)
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
