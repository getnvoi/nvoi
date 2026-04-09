package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/nvoi/internal/render"
)

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
