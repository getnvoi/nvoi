package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
)

type SSHRequest struct {
	AppName     string
	Env         string
	Provider    string
	Credentials map[string]string
	SSHKey      []byte
	Command     []string
}

func SSH(ctx context.Context, req SSHRequest) error {
	names, err := core.NewNames(req.AppName, req.Env)
	if err != nil {
		return err
	}
	prov, err := provider.ResolveCompute(req.Provider, req.Credentials)
	if err != nil {
		return err
	}

	master, err := FindMaster(ctx, prov, names)
	if err != nil {
		return err
	}

	ssh, err := infra.ConnectSSH(ctx, master.IPv4+":22", core.DefaultUser, req.SSHKey)
	if err != nil {
		return fmt.Errorf("ssh %s: %w", master.IPv4, err)
	}
	defer ssh.Close()

	cmd := strings.Join(req.Command, " ")
	return ssh.RunStream(ctx, cmd, os.Stdout, os.Stderr)
}
