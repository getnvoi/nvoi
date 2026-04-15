package cloud

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewReposCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repos",
		Short: "Manage repos",
	}

	cmd.AddCommand(newReposListCmd())
	cmd.AddCommand(newReposCreateCmd())
	cmd.AddCommand(newReposUseCmd())
	cmd.AddCommand(newReposDeleteCmd())

	return cmd
}

func requireWorkspace(cfg *AuthConfig) (string, error) {
	if cfg.WorkspaceID == "" {
		return "", fmt.Errorf("no active workspace — run 'nvoi workspaces use <name>'")
	}
	return cfg.WorkspaceID, nil
}

func RequireRepo(cfg *AuthConfig) (string, string, error) {
	wsID, err := requireWorkspace(cfg)
	if err != nil {
		return "", "", err
	}
	if cfg.RepoID == "" {
		return "", "", fmt.Errorf("no active repo — run 'nvoi repos use <name>'")
	}
	return wsID, cfg.RepoID, nil
}

func newReposListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List repos in active workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			var repos []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("GET", "/workspaces/"+wsID+"/repos", nil, &repos); err != nil {
				return err
			}

			for _, r := range repos {
				marker := "  "
				if r.ID == cfg.RepoID {
					marker = "* "
				}
				fmt.Printf("%s%s\t%s\n", marker, r.Name, r.ID)
			}
			return nil
		},
	}
}

func newReposCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a repo in active workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			name := args[0]

			// Idempotent: if repo already exists, just use it.
			var existing []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("GET", "/workspaces/"+wsID+"/repos", nil, &existing); err != nil {
				return err
			}
			for _, r := range existing {
				if r.Name == name {
					cfg.RepoID = r.ID
					if err := SaveAuthConfig(cfg); err != nil {
						return err
					}
					fmt.Printf("repo %s already exists (%s)\n", r.Name, r.ID)
					return nil
				}
			}

			var repo struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("POST", "/workspaces/"+wsID+"/repos", map[string]string{"name": name}, &repo); err != nil {
				return err
			}

			cfg.RepoID = repo.ID
			if err := SaveAuthConfig(cfg); err != nil {
				return err
			}

			fmt.Printf("created repo %s (%s)\n", repo.Name, repo.ID)
			return nil
		},
	}
}

func newReposUseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "use <name-or-id>",
		Short: "Set active repo and optionally link providers",
		Long: `Sets the active repo. Pass --compute, --dns, --storage, --build, --secrets to link
providers from the workspace by alias.

Examples:
  nvoi repos use myapp
  nvoi repos use myapp --compute hetzner-prod --dns cf-dns --storage cf-storage --build daytona-team --secrets doppler`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			var repos []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if err := client.Do("GET", "/workspaces/"+wsID+"/repos", nil, &repos); err != nil {
				return err
			}

			target := args[0]
			var match *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			for i := range repos {
				if repos[i].ID == target || repos[i].Name == target {
					match = &repos[i]
					break
				}
			}
			if match == nil {
				return fmt.Errorf("repo %q not found in workspace", target)
			}

			cfg.RepoID = match.ID
			if err := SaveAuthConfig(cfg); err != nil {
				return err
			}

			// Link providers if any flags are set.
			body := map[string]string{}
			for _, kind := range providerKinds {
				if v, _ := cmd.Flags().GetString(kind); v != "" {
					body[kind+"_provider"] = v
				}
			}
			if len(body) > 0 {
				path := "/workspaces/" + wsID + "/repos/" + match.ID
				if err := client.Do("PUT", path, body, nil); err != nil {
					return err
				}
			}

			fmt.Printf("using repo %s (%s)\n", match.Name, match.ID)
			return nil
		},
	}
	for _, kind := range providerKinds {
		cmd.Flags().String(kind, "", "link "+kind+" provider by alias")
	}
	return cmd
}

func newReposDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := AuthedClient()
			if err != nil {
				return err
			}
			wsID, err := requireWorkspace(cfg)
			if err != nil {
				return err
			}

			if err := client.Do("DELETE", "/workspaces/"+wsID+"/repos/"+args[0], nil, nil); err != nil {
				return err
			}

			fmt.Println("deleted")
			return nil
		},
	}
}
