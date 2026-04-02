package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/getnvoi/nvoi/internal/app"
	"github.com/spf13/cobra"
)

func newDescribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Describe the cluster — nodes, workloads, pods, services, ingress, secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			appName, env, err := resolveAppEnv(cmd)
			if err != nil {
				return err
			}
			providerName, err := resolveComputeProvider(cmd)
			if err != nil {
				return err
			}
			creds, err := resolveComputeCredentials(cmd, providerName)
			if err != nil {
				return err
			}
			sshKey, err := resolveSSHKey()
			if err != nil {
				return err
			}

			req := app.DescribeRequest{
				Cluster: app.Cluster{
					AppName:     appName,
					Env:         env,
					Provider:    providerName,
					Credentials: creds,
					SSHKey:      sshKey,
				},
			}

			jsonOutput, _ := cmd.Flags().GetBool("json")
			if jsonOutput {
				raw, err := app.DescribeJSON(cmd.Context(), req)
				if err != nil {
					return err
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(raw)
			}

			res, err := app.Describe(cmd.Context(), req)
			if err != nil {
				return err
			}

			fmt.Println()
			fmt.Println(titleStyle.Render(res.Namespace))

			g := NewTableGroup()

			t := g.Add("NODES", "NAME", "STATUS", "ROLE", "IP")
			for _, n := range res.Nodes {
				t.Row(n.Name, n.Status, n.Role, n.IP)
			}

			t = g.Add("WORKLOADS", "NAME", "KIND", "READY", "IMAGE", "AGE")
			for _, w := range res.Workloads {
				t.Row(w.Name, w.Kind, w.Ready, w.Image, w.Age)
			}

			t = g.Add("PODS", "NAME", "STATUS", "NODE", "RESTARTS", "AGE")
			for _, p := range res.Pods {
				t.Row(p.Name, p.Status, p.Node, fmt.Sprintf("%d", p.Restarts), p.Age)
			}

			t = g.Add("SERVICES", "NAME", "TYPE", "CLUSTER-IP", "PORTS")
			for _, s := range res.Services {
				t.Row(s.Name, s.Type, s.ClusterIP, s.Ports)
			}

			if len(res.Ingress) > 0 {
				t = g.Add("INGRESS", "DOMAIN", "SERVICE", "PORT")
				for _, i := range res.Ingress {
					t.Row(i.Domain, i.Service, fmt.Sprintf("%d", i.Port))
				}
			}

			if len(res.Secrets) > 0 {
				t = g.Add("SECRETS", "KEY", "VALUE")
				for _, s := range res.Secrets {
					t.Row(s.Key, s.Value)
				}
			}

			if len(res.Storage) > 0 {
				t = g.Add("STORAGE", "NAME", "BUCKET")
				for _, s := range res.Storage {
					t.Row(s.Name, s.Bucket)
				}
			}

			g.Print()
			fmt.Println(dimStyle.Render(fmt.Sprintf("  generated at %s", time.Now().Format("2006-01-02 15:04:05"))))
			fmt.Println()
			return nil
		},
	}
	addComputeProviderFlags(cmd)
	addAppFlags(cmd)
	cmd.Flags().Bool("json", false, "output raw kubectl JSON")
	return cmd
}
