package render

import (
	"fmt"
	"time"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
)

// RenderDescribe prints a DescribeResult as a table group.
func RenderDescribe(res *pkgcore.DescribeResult) {
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

	if res.Tunnel != nil {
		t = g.Add(fmt.Sprintf("TUNNEL (%s)", res.Tunnel.Provider), "DOMAIN", "SERVICE", "PORT")
		for _, r := range res.Tunnel.Routes {
			t.Row(r.Domain, r.Service, fmt.Sprintf("%d", r.Port))
		}
		if len(res.Tunnel.Agents) > 0 {
			t = g.Add("TUNNEL AGENTS", "NAME", "STATUS", "RESTARTS", "AGE")
			for _, a := range res.Tunnel.Agents {
				t.Row(a.Name, a.Status, fmt.Sprintf("%d", a.Restarts), a.Age)
			}
		}
	}

	if len(res.Secrets) > 0 {
		t = g.Add("SECRETS", "KEY", "SERVICE")
		for _, s := range res.Secrets {
			t.Row(s.Key, s.Service)
		}
	}

	if len(res.Storage) > 0 {
		t = g.Add("STORAGE", "NAME", "BUCKET")
		for _, s := range res.Storage {
			t.Row(s.Name, s.Bucket)
		}
	}

	g.Print()
	fmt.Println(DimStyle.Render(fmt.Sprintf("  retrieved from project '%s'", res.Namespace)))
	fmt.Println(DimStyle.Render(fmt.Sprintf("  generated at %s", time.Now().Format("2006-01-02 15:04:05"))))
	fmt.Println()
}
