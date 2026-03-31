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
	return cmd
}

func newSecretSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Create or update a k8s secret",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]

			_ = key
			_ = value

			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl create secret generic (dry-run + apply on remote)
			return fmt.Errorf("not implemented")
		},
	}
}

func newSecretDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [key]",
		Short: "Remove a secret key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. Rebuild k8s secret without this key (kubectl on remote)
			return fmt.Errorf("not implemented")
		},
	}
}

func newSecretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret keys (values stored in k8s only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO Phase 3:
			// 1. Resolve master from provider API → SSH
			// 2. kubectl get secret -o jsonpath on remote — list keys (not values)
			return fmt.Errorf("not implemented")
		},
	}
}
