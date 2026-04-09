package managed

import "fmt"

func init() {
	Register(postgresDefinition{})
}

type postgresDefinition struct{}

func (postgresDefinition) Kind() string     { return "postgres" }
func (postgresDefinition) Category() string { return "database" }

func (postgresDefinition) Shape(name string) BundleShape {
	ns := namespaced(name)
	return BundleShape{
		Kind:          "postgres",
		RootService:   name,
		OwnedChildren: []string{name, name + "-backup", name + "-backups", name + "-data"},
		Crons:         []string{name + "-backup"},
		Services:      []string{name},
		Storages:      []string{name + "-backups"},
		Volumes:       []string{name + "-data"},
		SecretKeys: []string{
			"POSTGRES_PASSWORD_" + ns,
			"DATABASE_" + ns + "_HOST",
			"DATABASE_" + ns + "_NAME",
			"DATABASE_" + ns + "_PASSWORD",
			"DATABASE_" + ns + "_PORT",
			"DATABASE_" + ns + "_URL",
			"DATABASE_" + ns + "_USER",
		},
	}
}

func (postgresDefinition) Compile(req Request) (Result, error) {
	password, err := requireEnv(req.Env, "POSTGRES_PASSWORD", "postgres", req.Name)
	if err != nil {
		return Result{}, err
	}

	creds := map[string]string{
		"HOST":     req.Name,
		"PORT":     "5432",
		"USER":     "postgres",
		"PASSWORD": password,
		"NAME":     req.Name,
		"URL":      fmt.Sprintf("postgres://postgres:%s@%s:5432/%s", password, req.Name, req.Name),
	}

	internalKey := "POSTGRES_PASSWORD_" + namespaced(req.Name)
	exported := map[string]string{}
	for _, key := range sortedKeys(creds) {
		exported["DATABASE_"+namespaced(req.Name)+"_"+key] = creds[key]
	}

	volume := Volume{
		Name:   req.Name + "-data",
		SizeGB: 10,
		Server: req.Context.DefaultVolumeServer,
	}
	service := Service{
		Name:  req.Name,
		Image: "postgres:17",
		Port:  5432,
		Volumes: []string{
			volume.Name + ":/var/lib/postgresql/data",
		},
		Env: []string{
			"POSTGRES_DB=" + req.Name,
			"POSTGRES_USER=postgres",
		},
		Secrets: []string{
			"POSTGRES_PASSWORD=" + internalKey,
		},
	}
	storage := Storage{
		Name:       req.Name + "-backups",
		ExpireDays: 30,
	}
	cron := Cron{
		Name:     req.Name + "-backup",
		Schedule: "0 2 * * *",
		Image:    "postgres:17",
		Command:  fmt.Sprintf("pg_dump -h %s -U postgres %s > /tmp/%s.sql", req.Name, req.Name, req.Name),
		Env: []string{
			"PGPASSWORD=" + password,
		},
		Secrets: []string{
			"POSTGRES_PASSWORD=" + internalKey,
		},
		Storage: []string{
			storage.Name,
		},
	}

	ops := []Operation{
		{
			Kind: "secret.set",
			Name: internalKey,
			Params: map[string]any{
				"value": password,
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: internalKey},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_HOST",
			Params: map[string]any{
				"value": creds["HOST"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_HOST"},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_PORT",
			Params: map[string]any{
				"value": creds["PORT"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_PORT"},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_USER",
			Params: map[string]any{
				"value": creds["USER"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_USER"},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_PASSWORD",
			Params: map[string]any{
				"value": creds["PASSWORD"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_PASSWORD"},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_NAME",
			Params: map[string]any{
				"value": creds["NAME"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_NAME"},
		},
		{
			Kind: "secret.set",
			Name: "DATABASE_" + namespaced(req.Name) + "_URL",
			Params: map[string]any{
				"value": creds["URL"],
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_URL"},
		},
		{
			Kind: "storage.set",
			Name: storage.Name,
			Params: map[string]any{
				"expire_days": storage.ExpireDays,
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: storage.Name},
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
		{
			Kind: "cron.set",
			Name: cron.Name,
			Params: map[string]any{
				"command":  cron.Command,
				"env":      append([]string{}, cron.Env...),
				"image":    cron.Image,
				"schedule": cron.Schedule,
				"secrets":  append([]string{}, cron.Secrets...),
				"storage":  append([]string{}, cron.Storage...),
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: cron.Name},
		},
	}
	return Result{
		Bundle: Bundle{
			Kind:        req.Kind,
			RootService: req.Name,
			OwnedChildren: []string{
				req.Name,
				req.Name + "-backup",
				req.Name + "-backups",
				req.Name + "-data",
			},
			InternalSecrets: map[string]string{internalKey: password},
			ExportedSecrets: exported,
			Volumes:         []Volume{volume},
			Storages:        []Storage{storage},
			Services:        []Service{service},
			Crons:           []Cron{cron},
			Operations:      ops,
		},
	}, nil
}
