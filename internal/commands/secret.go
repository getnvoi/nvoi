package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewSecretCmd returns the secret command group.
func NewSecretCmd(b Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage k8s secrets",
	}
	cmd.AddCommand(newSecretSetCmd(b))
	cmd.AddCommand(newSecretDeleteCmd(b))
	cmd.AddCommand(newSecretListCmd(b))
	cmd.AddCommand(newSecretRevealCmd(b))
	return cmd
}

func newSecretSetCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Store a secret value in the cluster",
		Long: `Stores a key-value pair in the k8s Secret. Services reference
secrets by key name via --secret KEY on service set.

  nvoi secret set RAILS_MASTER_KEY abc123
  nvoi service set web --image ... --secret RAILS_MASTER_KEY`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.SecretSet(cmd.Context(), args[0], args[1])
		},
	}
}

func newSecretDeleteCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "delete [key]",
		Short: "Remove a secret key from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.SecretDelete(cmd.Context(), args[0])
		},
	}
}

func newSecretListCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret key names (values hidden)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.SecretList(cmd.Context())
		},
	}
}

func newSecretRevealCmd(b Backend) *cobra.Command {
	return &cobra.Command{
		Use:   "reveal [key]",
		Short: "Show a secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := b.SecretReveal(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Println(value)
			return nil
		},
	}
}
