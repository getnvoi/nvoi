package managed

import "fmt"

func init() {
	Register(claudeDefinition{})
}

type claudeDefinition struct{}

func (claudeDefinition) Kind() string     { return "claude" }
func (claudeDefinition) Category() string { return "agent" }

func (claudeDefinition) Shape(name string) BundleShape {
	ns := namespaced(name)
	return BundleShape{
		Kind:          "claude",
		RootService:   name,
		OwnedChildren: []string{name, name + "-data"},
		Services:      []string{name},
		Volumes:       []string{name + "-data"},
		SecretKeys: []string{
			"NVOI_AGENT_TOKEN_" + ns,
			"AGENT_" + ns + "_HOST",
			"AGENT_" + ns + "_PORT",
			"AGENT_" + ns + "_TOKEN",
			"AGENT_" + ns + "_URL",
		},
	}
}

func (claudeDefinition) Compile(req Request) (Result, error) {
	token, err := requireEnv(req.Env, "NVOI_AGENT_TOKEN", "claude", req.Name)
	if err != nil {
		return Result{}, err
	}

	creds := map[string]string{
		"HOST":  req.Name,
		"PORT":  "8080",
		"TOKEN": token,
		"URL":   fmt.Sprintf("http://%s:8080", req.Name),
	}

	internalKey := "NVOI_AGENT_TOKEN_" + namespaced(req.Name)
	exported := map[string]string{}
	for _, key := range sortedKeys(creds) {
		exported["AGENT_"+namespaced(req.Name)+"_"+key] = creds[key]
	}

	volume := Volume{
		Name:   req.Name + "-data",
		SizeGB: 10,
		Server: req.Context.DefaultVolumeServer,
	}
	service := Service{
		Name:  req.Name,
		Image: "ghcr.io/getnvoi/nvoi-agent:latest",
		Port:  8080,
		Volumes: []string{
			volume.Name + ":/var/lib/nvoi-agent",
		},
		Env: []string{
			"NVOI_AGENT_NAME=" + req.Name,
		},
		Secrets: []string{
			"NVOI_AGENT_TOKEN=" + internalKey,
		},
	}

	ops := []Operation{
		{
			Kind: "secret.set",
			Name: internalKey,
			Params: map[string]any{
				"value": token,
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: internalKey},
		},
		{
			Kind: "secret.set",
			Name: "AGENT_" + namespaced(req.Name) + "_HOST",
			Params: map[string]any{
				"value": creds["HOST"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "AGENT_" + namespaced(req.Name) + "_HOST"},
		},
		{
			Kind: "secret.set",
			Name: "AGENT_" + namespaced(req.Name) + "_PORT",
			Params: map[string]any{
				"value": creds["PORT"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "AGENT_" + namespaced(req.Name) + "_PORT"},
		},
		{
			Kind: "secret.set",
			Name: "AGENT_" + namespaced(req.Name) + "_TOKEN",
			Params: map[string]any{
				"value": creds["TOKEN"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "AGENT_" + namespaced(req.Name) + "_TOKEN"},
		},
		{
			Kind: "secret.set",
			Name: "AGENT_" + namespaced(req.Name) + "_URL",
			Params: map[string]any{
				"value": creds["URL"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "AGENT_" + namespaced(req.Name) + "_URL"},
		},
		{
			Kind: "volume.set",
			Name: volume.Name,
			Params: map[string]any{
				"server": volume.Server,
				"size":   volume.SizeGB,
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: volume.Name},
		},
		{
			Kind: "service.set",
			Name: service.Name,
			Params: map[string]any{
				"env":          append([]string{}, service.Env...),
				"image":        service.Image,
				"port":         service.Port,
				"secrets":      append([]string{}, service.Secrets...),
				"volumes":      append([]string{}, service.Volumes...),
				"managed_kind": req.Kind,
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: service.Name},
		},
	}
	return Result{
		Bundle: Bundle{
			Kind:            req.Kind,
			RootService:     req.Name,
			OwnedChildren:   []string{req.Name, volume.Name},
			InternalSecrets: map[string]string{internalKey: token},
			ExportedSecrets: exported,
			Volumes:         []Volume{volume},
			Services:        []Service{service},
			Operations:      ops,
		},
	}, nil
}
