package reconcile

import (
	"sort"
	"strings"

	"github.com/getnvoi/nvoi/internal/config"
)

// topoSortServices returns service names in dependency order: every
// service's `depends_on` entries come before the service itself. Services
// without deps appear in alphabetical order to keep the output stable.
//
// Assumes the graph is acyclic. Run findDependencyCycle at validate time
// before calling this — a cycle here would just silently drop the cycle
// members from the output.
func topoSortServices(services map[string]config.ServiceDef) []string {
	// Build reverse adjacency: dep -> services that depend on it.
	// Edges are "dep → dependent" so Kahn starts from deps with 0 in-degree.
	inDegree := make(map[string]int, len(services))
	dependents := make(map[string][]string, len(services))
	for name := range services {
		inDegree[name] = 0
	}
	for name, svc := range services {
		for _, dep := range svc.DependsOn {
			if _, ok := services[dep]; !ok {
				// validate.go already rejects unknown deps; defensive skip.
				continue
			}
			inDegree[name]++
			dependents[dep] = append(dependents[dep], name)
		}
	}

	// Kahn's algorithm, pulling alphabetically smallest ready node first
	// for deterministic output when multiple nodes are eligible.
	var ready []string
	for name, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)

	out := make([]string, 0, len(services))
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)

		// Newly unlocked dependents — gather, sort, prepend in sorted order.
		var unlocked []string
		for _, d := range dependents[n] {
			inDegree[d]--
			if inDegree[d] == 0 {
				unlocked = append(unlocked, d)
			}
		}
		if len(unlocked) > 0 {
			ready = append(ready, unlocked...)
			sort.Strings(ready)
		}
	}
	return out
}

// findDependencyCycle returns a human-readable description of the first
// depends_on cycle found, or "" if the graph is acyclic.
func findDependencyCycle(services map[string]config.ServiceDef) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(services))
	var path []string
	var walk func(n string) []string
	walk = func(n string) []string {
		color[n] = gray
		path = append(path, n)
		for _, dep := range services[n].DependsOn {
			if _, ok := services[dep]; !ok {
				continue
			}
			switch color[dep] {
			case gray:
				// Cycle. Slice path from first occurrence of dep.
				for i, p := range path {
					if p == dep {
						cyc := append([]string{}, path[i:]...)
						cyc = append(cyc, dep) // close the loop visually
						return cyc
					}
				}
				return append([]string{}, path...)
			case white:
				if cyc := walk(dep); cyc != nil {
					return cyc
				}
			}
		}
		color[n] = black
		path = path[:len(path)-1]
		return nil
	}

	// Deterministic start order.
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		if color[n] == white {
			if cyc := walk(n); cyc != nil {
				return strings.Join(cyc, " -> ")
			}
		}
	}
	return ""
}
