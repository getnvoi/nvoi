package cloud

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show current user and context",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadAuthConfig()
			if err != nil {
				return err
			}
			fmt.Printf("user:      %s\n", cfg.Username)
			fmt.Printf("api:       %s\n", cfg.APIBase)
			if cfg.WorkspaceID != "" {
				fmt.Printf("workspace: %s\n", cfg.WorkspaceID)
			}
			if cfg.RepoID != "" {
				fmt.Printf("repo:      %s\n", cfg.RepoID)
			}
			return nil
		},
	}
}
