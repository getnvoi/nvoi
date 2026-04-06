package cli

import (
	"bufio"
	"fmt"

	"github.com/spf13/cobra"
)

func newSSHCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ssh <command...>",
		Short: "Run a command on the master node via SSH",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := authedClient()
			if err != nil {
				return err
			}
			wsID, repoID, err := requireRepo(cfg)
			if err != nil {
				return err
			}

			path := "/workspaces/" + wsID + "/repos/" + repoID + "/ssh"
			resp, err := client.doRawWithBody("POST", path, map[string]any{"command": args})
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				fmt.Println(scanner.Text())
			}
			return scanner.Err()
		},
	}
}
