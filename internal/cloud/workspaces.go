package cloud

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewWorkspacesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workspaces",
		Aliases: []string{"ws"},
		Short:   "Manage workspaces",
	}

	cmd.AddCommand(newWorkspacesListCmd())
	cmd.AddCommand(newWorkspacesCreateCmd())
	cmd.AddCommand(newWorkspacesUseCmd())
	cmd.AddCommand(newWorkspacesDeleteCmd())

	return cmd
}

func newWorkspacesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}

			var workspaces []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("GET", "/workspaces", nil, &workspaces); err != nil {
				return err
			}

			for _, ws := range workspaces {
				marker := "  "
				if ws.ID == cfg.WorkspaceID {
					marker = "* "
				}
				fmt.Printf("%s%s\t%s\n", marker, ws.Name, ws.ID)
			}
			return nil
		},
	}
}

func newWorkspacesCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := AuthedClient()
			if err != nil {
				return err
			}

			var ws struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("POST", "/workspaces", map[string]string{"name": args[0]}, &ws); err != nil {
				return err
			}

			fmt.Printf("created workspace %s (%s)\n", ws.Name, ws.ID)
			return nil
		},
	}
}

func newWorkspacesUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name-or-id>",
		Short: "Set active workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}

			// Resolve: try as ID first, then search by name.
			var workspaces []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("GET", "/workspaces", nil, &workspaces); err != nil {
				return err
			}

			target := args[0]
			var match *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			for i := range workspaces {
				if workspaces[i].ID == target || workspaces[i].Name == target {
					match = &workspaces[i]
					break
				}
			}
			if match == nil {
				return fmt.Errorf("workspace %q not found", target)
			}

			cfg.WorkspaceID = match.ID
			cfg.RepoID = "" // reset repo on workspace switch
			if err := SaveAuthConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("using workspace %s (%s)\n", match.Name, match.ID)
			return nil
		},
	}
}

func newWorkspacesDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := AuthedClient()
			if err != nil {
				return err
			}

			var resp struct{ Name string }
			if err := client.Do("DELETE", "/workspaces/"+args[0], nil, &resp); err != nil {
				return err
			}

			fmt.Printf("deleted workspace %s\n", resp.Name)
			return nil
		},
	}
}
