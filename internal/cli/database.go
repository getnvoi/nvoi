package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

func (c *CloudBackend) DatabaseList(ctx context.Context) error {
	var services []pkgcore.ManagedService
	if err := c.client.Do("GET", c.repoPath("/database"), nil, &services); err != nil {
		return err
	}
	if len(services) == 0 {
		fmt.Println("no managed databases found")
		return nil
	}
	for _, svc := range services {
		children := strings.Join(svc.Children, ", ")
		fmt.Printf("%s  type=%s  %s  %s  children=[%s]\n", svc.Name, svc.ManagedKind, svc.Image, svc.Ready, children)
	}
	return nil
}

func (c *CloudBackend) BackupCreate(ctx context.Context, name, kind string) error {
	var resp struct{ Status string }
	if err := c.client.Do("POST", c.repoPath("/database/"+esc(name)+"/backup/create"), nil, &resp); err != nil {
		return err
	}
	fmt.Printf("backup %s\n", resp.Status)
	return nil
}

func (c *CloudBackend) BackupList(ctx context.Context, name, kind, backupStorage string) error {
	var artifacts []pkgcore.BackupArtifact
	if err := c.client.Do("GET", c.repoPath("/database/"+esc(name)+"/backup"), nil, &artifacts); err != nil {
		return err
	}
	if len(artifacts) == 0 {
		fmt.Println("no backups found")
		return nil
	}
	for _, a := range artifacts {
		fmt.Printf("%s  %d bytes  %s\n", a.Key, a.Size, a.LastModified)
	}
	return nil
}

func (c *CloudBackend) BackupDownload(ctx context.Context, name, kind, backupStorage, key string) error {
	resp, err := c.client.doRaw("GET", c.repoPath("/database/"+esc(name)+"/backup/"+esc(key)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}
