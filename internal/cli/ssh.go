package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/getnvoi/nvoi/internal/commands"
)

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
