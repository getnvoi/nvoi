package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/getnvoi/nvoi/internal/core"
	"github.com/getnvoi/nvoi/internal/infra"
	"github.com/getnvoi/nvoi/internal/provider"
	_ "github.com/getnvoi/nvoi/internal/provider/hetzner" // register
	"github.com/spf13/cobra"
)

func newComputeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Manage compute servers",
	}
	cmd.AddCommand(newComputeSetCmd())
	cmd.AddCommand(newComputeDeleteCmd())
	cmd.AddCommand(newComputeListCmd())
	return cmd
}

func newComputeSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [name]",
		Short: "Provision or reconcile a server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			providerName, _ := cmd.Flags().GetString("provider")
			serverType, _ := cmd.Flags().GetString("type")
			region, _ := cmd.Flags().GetString("region")

			names, err := core.NewNames()
			if err != nil {
				return err
			}
			prov, err := provider.ResolveCompute(providerName)
			if err != nil {
				return err
			}

			// Load SSH key
			keyPath := os.Getenv("SSH_KEY_PATH")
			if keyPath == "" {
				keyPath = os.ExpandEnv("$HOME/.ssh/id_ed25519")
			}
			privKey, err := os.ReadFile(keyPath)
			if err != nil {
				return fmt.Errorf("read SSH key %s: %w", keyPath, err)
			}
			pubKey, err := core.DerivePublicKey(privKey)
			if err != nil {
				return fmt.Errorf("derive public key: %w", err)
			}

			// Cloud-init
			userData, err := infra.RenderCloudInit(strings.TrimSpace(pubKey))
			if err != nil {
				return err
			}

			fmt.Printf("==> compute set %s\n", names.Server(name))

			// EnsureServer — provider resolves firewall + network internally
			srv, err := prov.EnsureServer(cmd.Context(), provider.CreateServerRequest{
				Name:       names.Server(name),
				ServerType: serverType,
				Image:      core.DefaultImage,
				Location:   region,
				UserData:   userData,
				Labels:     names.Labels(),
			})
			if err != nil {
				return err
			}

			// Wait for SSH
			fmt.Printf("  waiting for SSH on %s...\n", srv.IPv4)
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			if err := infra.WaitSSH(ctx, srv.IPv4+":22", privKey); err != nil {
				return fmt.Errorf("SSH not reachable: %w", err)
			}
			fmt.Printf("  ✓ SSH ready\n")

			// Ensure Docker
			fmt.Printf("  ensuring Docker...\n")
			if err := infra.EnsureDocker(cmd.Context(), srv.IPv4, privKey); err != nil {
				return fmt.Errorf("docker: %w", err)
			}
			fmt.Printf("  ✓ Docker ready\n")

			fmt.Printf("  ✓ %s %s (private: %s)\n", names.Server(name), srv.IPv4, srv.PrivateIP)
			return nil
		},
	}
	cmd.Flags().String("provider", "", "compute provider (hetzner)")
	cmd.Flags().String("type", "", "server instance type (e.g. cpx11)")
	cmd.Flags().String("region", "", "server region (e.g. fsn1)")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("region")
	return cmd
}

func newComputeDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a server (firewall + network cleaned up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			providerName, _ := cmd.Flags().GetString("provider")
			yes, _ := cmd.Flags().GetBool("yes")

			names, err := core.NewNames()
			if err != nil {
				return err
			}

			serverName := names.Server(name)
			if !yes {
				fmt.Printf("Delete server %s? [y/N] ", serverName)
				var confirm string
				fmt.Scanln(&confirm)
				if confirm != "y" && confirm != "yes" {
					fmt.Println("aborted.")
					return nil
				}
			}

			prov, err := provider.ResolveCompute(providerName)
			if err != nil {
				return err
			}

			fmt.Printf("==> compute delete %s\n", serverName)
			if err := prov.DeleteServer(cmd.Context(), provider.DeleteServerRequest{
				Name:   serverName,
				Labels: names.Labels(),
			}); err != nil {
				return err
			}
			fmt.Printf("  ✓ deleted\n")
			return nil
		},
	}
	cmd.Flags().String("provider", "", "compute provider (hetzner)")
	cmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}

func newComputeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List provisioned servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			providerName, _ := cmd.Flags().GetString("provider")

			names, err := core.NewNames()
			if err != nil {
				return err
			}
			prov, err := provider.ResolveCompute(providerName)
			if err != nil {
				return err
			}

			servers, err := prov.ListServers(cmd.Context(), names.Labels())
			if err != nil {
				return err
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintf(tw, "NAME\tSTATUS\tIPv4\tPRIVATE IP\n")
			for _, s := range servers {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, s.Status, s.IPv4, s.PrivateIP)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().String("provider", "", "compute provider (hetzner)")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}
