package managed

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/getnvoi/nvoi/pkg/utils"
)

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
		OwnedChildren: []string{name, name + "-backup", name + "-data"},
		Crons:         []string{name + "-backup"},
		Services:      []string{name},
		Volumes:       []string{name + "-data"},
		SecretKeys: []string{
			"POSTGRES_DB_" + ns,
			"POSTGRES_PASSWORD_" + ns,
			"POSTGRES_USER_" + ns,
			"DATABASE_" + ns + "_HOST",
			"DATABASE_" + ns + "_NAME",
			"DATABASE_" + ns + "_PASSWORD",
			"DATABASE_" + ns + "_PORT",
			"DATABASE_" + ns + "_URL",
			"DATABASE_" + ns + "_USER",
		},
	}
}

const defaultPostgresImage = "postgres:17"

func (postgresDefinition) Compile(req Request) (Result, error) {
	password, err := requireEnv(req.Env, "POSTGRES_PASSWORD", "postgres", req.Name)
	if err != nil {
		return Result{}, err
	}
	user, err := requireEnv(req.Env, "POSTGRES_USER", "postgres", req.Name)
	if err != nil {
		return Result{}, err
	}
	dbName, err := requireEnv(req.Env, "POSTGRES_DB", "postgres", req.Name)
	if err != nil {
		return Result{}, err
	}

	image := utils.GetString(req.Params, "image")
	if image == "" {
		image = defaultPostgresImage
	}

	creds := map[string]string{
		"HOST":     req.Name,
		"PORT":     "5432",
		"USER":     user,
		"PASSWORD": password,
		"NAME":     dbName,
		"URL":      fmt.Sprintf("postgres://%s:%s@%s:5432/%s", url.PathEscape(user), url.PathEscape(password), req.Name, url.PathEscape(dbName)),
	}

	internalKey := "POSTGRES_PASSWORD_" + namespaced(req.Name)
	userKey := "POSTGRES_USER_" + namespaced(req.Name)
	dbKey := "POSTGRES_DB_" + namespaced(req.Name)

	exported := map[string]string{}
	for _, key := range sortedKeys(creds) {
		exported["DATABASE_"+namespaced(req.Name)+"_"+key] = creds[key]
	}

	volumeSize := utils.GetInt(req.Params, "volume_size")
	if volumeSize == 0 {
		volumeSize = 10
	}
	volume := Volume{
		Name:   req.Name + "-data",
		SizeGB: volumeSize,
		Server: req.Context.DefaultVolumeServer,
	}
	service := Service{
		Name:  req.Name,
		Image: image,
		Port:  5432,
		Volumes: []string{
			volume.Name + ":/var/lib/postgresql/data",
		},
		Secrets: []string{
			"POSTGRES_PASSWORD=" + internalKey,
			"POSTGRES_USER=" + userKey,
			"POSTGRES_DB=" + dbKey,
		},
	}

	ops := []Operation{
		{Kind: "secret.set", Name: internalKey, Params: map[string]any{"value": password},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: internalKey}},
		{Kind: "secret.set", Name: userKey, Params: map[string]any{"value": user},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: userKey}},
		{Kind: "secret.set", Name: dbKey, Params: map[string]any{"value": dbName},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: dbKey}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_HOST", Params: map[string]any{"value": creds["HOST"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_HOST"}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_PORT", Params: map[string]any{"value": creds["PORT"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_PORT"}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_USER", Params: map[string]any{"value": creds["USER"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_USER"}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_PASSWORD", Params: map[string]any{"value": creds["PASSWORD"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_PASSWORD"}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_NAME", Params: map[string]any{"value": creds["NAME"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_NAME"}},
		{Kind: "secret.set", Name: "DATABASE_" + namespaced(req.Name) + "_URL", Params: map[string]any{"value": creds["URL"]},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: "DATABASE_" + namespaced(req.Name) + "_URL"}},
		{Kind: "volume.set", Name: volume.Name, Params: map[string]any{"server": volume.Server, "size": volume.SizeGB},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: volume.Name}},
		{Kind: "service.set", Name: service.Name, Params: map[string]any{
			"env": append([]string{}, service.Env...), "image": service.Image,
			"port": service.Port, "secrets": append([]string{}, service.Secrets...),
			"volumes": append([]string{}, service.Volumes...), "managed_kind": req.Kind,
		}, Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: service.Name}},
	}

	// Backup cron — only when backup image and schedule are provided.
	backupImage := utils.GetString(req.Params, "backup_image")
	backupCron := utils.GetString(req.Params, "backup_cron")
	backupStorage := utils.GetString(req.Params, "backup_storage")

	var crons []Cron
	if backupImage != "" && backupCron != "" {
		prefix := "STORAGE_" + strings.ToUpper(strings.ReplaceAll(backupStorage, "-", "_"))
		script := backupScript(req.Name, prefix)

		cron := Cron{
			Name:     req.Name + "-backup",
			Schedule: backupCron,
			Image:    backupImage,
			Command:  script,
			Server:   req.Context.DefaultVolumeServer,
			Secrets: []string{
				"POSTGRES_PASSWORD=" + internalKey,
				"POSTGRES_USER=" + userKey,
				"POSTGRES_DB=" + dbKey,
			},
			Storage: []string{
				backupStorage,
			},
		}
		crons = append(crons, cron)
		ops = append(ops, Operation{
			Kind: "cron.set", Name: cron.Name, Params: map[string]any{
				"command":  cron.Command,
				"env":      append([]string{}, cron.Env...),
				"image":    cron.Image,
				"schedule": cron.Schedule,
				"server":   cron.Server,
				"secrets":  append([]string{}, cron.Secrets...),
				"storage":  append([]string{}, cron.Storage...),
			},
			Owner: Ownership{ManagedKind: req.Kind, RootService: req.Name, ChildName: cron.Name},
		})
	}

	ownedChildren := []string{req.Name, req.Name + "-data"}
	if len(crons) > 0 {
		ownedChildren = append(ownedChildren, req.Name+"-backup")
	}

	return Result{
		Bundle: Bundle{
			Kind:            req.Kind,
			RootService:     req.Name,
			OwnedChildren:   ownedChildren,
			InternalSecrets: map[string]string{internalKey: password, userKey: user, dbKey: dbName},
			ExportedSecrets: exported,
			Volumes:         []Volume{volume},
			Services:        []Service{service},
			Crons:           crons,
			Operations:      ops,
		},
	}, nil
}

// backupScript returns a shell script that pipes pg_dump to S3 via aws cli.
// aws cli is pre-installed in the backup image (nvoi-pg-backup).
// Storage env vars (endpoint, bucket, access key, secret key) are injected
// by the --storage reference on the cron.
func backupScript(host, storagePrefix string) string {
	return fmt.Sprintf(
		`set -e && `+
			`export AWS_ACCESS_KEY_ID=$%s_ACCESS_KEY_ID && `+
			`export AWS_SECRET_ACCESS_KEY=$%s_SECRET_ACCESS_KEY && `+
			`export PGPASSWORD=$POSTGRES_PASSWORD && `+
			`TIMESTAMP=$(date +%%Y%%m%%d-%%H%%M%%S) && `+
			`pg_dump -h %s -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-acl | gzip | `+
			`aws s3 cp - "s3://$%s_BUCKET/backup-$TIMESTAMP.sql.gz" --endpoint-url "$%s_ENDPOINT"`,
		storagePrefix, storagePrefix, host, storagePrefix, storagePrefix,
	)
}
