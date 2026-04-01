package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage k8s secrets",
	}
	cmd.AddCommand(newSecretSetCmd())
	cmd.AddCommand(newSecretDeleteCmd())
	cmd.AddCommand(newSecretListCmd())
	cmd.AddCommand(newSecretRevealCmd())
	return cmd
}

func newSecretSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Store a secret value in the cluster",
		Long: `Stores a key-value pair in the k8s Secret. Services reference
secrets by key name via --secret KEY on service set.

  nvoi secret set RAILS_MASTER_KEY abc123
  nvoi service set web --image ... --secret RAILS_MASTER_KEY`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]

			_, _, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			_, err = resolveComputeProvider(cmd)
			if err != nil {
				return err
			}

			_ = key
			_ = value

			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl create secret generic secrets --from-literal=KEY=VALUE --dry-run=client -o yaml | kubectl apply
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newSecretDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [key]",
		Short: "Remove a secret key from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. Rebuild k8s secret without this key (kubectl on remote)
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newSecretListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secret key names (values hidden)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl get secret secrets -o jsonpath='{.data}' on remote — list keys only
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}

func newSecretRevealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reveal [key]",
		Short: "Show a secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl get secret secrets -o jsonpath='{.data.KEY}' | base64 -d
			return fmt.Errorf("not implemented")
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	return cmd
}
