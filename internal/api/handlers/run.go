package handlers

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"github.com/getnvoi/nvoi/pkg/utils"
	"gorm.io/gorm"
)

// ── Input type ──────────────────────────────────────────────────────────────

type RunInput struct {
	RepoScopedInput
	Body struct {
		Kind   string         `json:"kind" required:"true" doc:"Step kind (instance.set, service.delete, etc.)"`
		Name   string         `json:"name" required:"true" doc:"Resource name (master, web, etc.)"`
		Params map[string]any `json:"params,omitempty" doc:"Step parameters"`
	}
}

// ── Handler ─────────────────────────────────────────────────────────────────

// Run executes a single command against the cluster and streams JSONL output.
// This is the thin line between the cloud CLI and pkg/core/.
func Run(db *gorm.DB) func(context.Context, *RunInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *RunInput) (*huma.StreamResponse, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		e, err := newRunner(repo, user)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "application/x-ndjson")
				w := ctx.BodyWriter()

				e.cluster.Output = &jsonlOutput{w: w}
				start := time.Now()

				execErr := e.dispatch(ctx.Context(), input.Body.Kind, input.Body.Name, input.Body.Params)

				// Log the command.
				status := "succeeded"
				errMsg := ""
				if execErr != nil {
					status = "failed"
					errMsg = execErr.Error()
					// Write error as final JSONL line.
					line := pkgcore.MarshalEvent(pkgcore.NewMessageEvent(pkgcore.EventError, execErr.Error()))
					w.Write([]byte(line + "\n"))
				}

				db.Create(&api.CommandLog{
					RepoID:     repo.ID,
					UserID:     user.ID,
					Kind:       input.Body.Kind,
					Name:       input.Body.Name,
					Status:     status,
					Error:      errMsg,
					DurationMs: int(time.Since(start).Milliseconds()),
				})
			},
		}, nil
	}
}

// ── runner ──────────────────────────────────────────────────────────────────

// runner holds per-request state: cluster + provider refs built from Repo's InfraProvider links.
type runner struct {
	cluster       pkgcore.Cluster
	dns           pkgcore.ProviderRef
	storage       pkgcore.ProviderRef
	buildProvider string
	buildCreds    map[string]string
	gitToken      string
}

func newRunner(repo *api.Repo, user *api.User) (*runner, error) {
	computeName, computeCreds := "", map[string]string(nil)
	if repo.ComputeProvider != nil {
		computeName = repo.ComputeProvider.Provider
		computeCreds = repo.ComputeProvider.CredentialsMap()
	}

	dnsName, dnsCreds := "", map[string]string(nil)
	if repo.DNSProvider != nil {
		dnsName = repo.DNSProvider.Provider
		dnsCreds = repo.DNSProvider.CredentialsMap()
	}

	storageName, storageCreds := "", map[string]string(nil)
	if repo.StorageProvider != nil {
		storageName = repo.StorageProvider.Provider
		storageCreds = repo.StorageProvider.CredentialsMap()
	}

	buildName, buildCreds := "", map[string]string(nil)
	if repo.BuildProvider != nil {
		buildName = repo.BuildProvider.Provider
		buildCreds = repo.BuildProvider.CredentialsMap()
	}

	return &runner{
		cluster: pkgcore.Cluster{
			AppName:     repo.Name,
			Env:         repo.Environment,
			Provider:    computeName,
			Credentials: computeCreds,
			SSHKey:      []byte(repo.SSHPrivateKey),
		},
		dns:           pkgcore.ProviderRef{Name: dnsName, Creds: dnsCreds},
		storage:       pkgcore.ProviderRef{Name: storageName, Creds: storageCreds},
		buildProvider: buildName,
		buildCreds:    buildCreds,
		gitToken:      user.GithubToken,
	}, nil
}

// dispatch maps a step kind to the corresponding pkg/core/ function.
func (e *runner) dispatch(ctx context.Context, kind, name string, params map[string]any) error {
	// Validate inputs at the API boundary — before anything touches infrastructure.
	if err := validateDispatchInput(kind, name, params); err != nil {
		return err
	}

	switch kind {
	case "firewall.set":
		allowed, err := parseFirewallFromParams(params)
		if err != nil {
			return err
		}
		return pkgcore.FirewallSet(ctx, pkgcore.FirewallSetRequest{
			Cluster:    e.cluster,
			AllowedIPs: allowed,
		})

	case "instance.set":
		_, err := pkgcore.ComputeSet(ctx, pkgcore.ComputeSetRequest{
			Cluster:    e.cluster,
			Name:       name,
			ServerType: utils.GetString(params, "type"),
			Region:     utils.GetString(params, "region"),
			Worker:     utils.GetString(params, "role") == "worker",
		})
		return err

	case "instance.delete":
		return pkgcore.ComputeDelete(ctx, pkgcore.ComputeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case "volume.set":
		_, err := pkgcore.VolumeSet(ctx, pkgcore.VolumeSetRequest{
			Cluster: e.cluster,
			Name:    name,
			Size:    utils.GetInt(params, "size"),
			Server:  utils.GetString(params, "server"),
		})
		return err

	case "volume.delete":
		return pkgcore.VolumeDelete(ctx, pkgcore.VolumeDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case "build":
		_, err := pkgcore.BuildRun(ctx, pkgcore.BuildRunRequest{
			Cluster:            e.cluster,
			Builder:            e.buildProvider,
			BuilderCredentials: e.buildCreds,
			Source:             utils.GetString(params, "source"),
			Name:               name,
			GitToken:           e.gitToken,
		})
		return err

	case "secret.set":
		return pkgcore.SecretSet(ctx, pkgcore.SecretSetRequest{
			Cluster: e.cluster,
			Key:     name,
			Value:   utils.GetString(params, "value"),
		})

	case "secret.delete":
		return pkgcore.SecretDelete(ctx, pkgcore.SecretDeleteRequest{
			Cluster: e.cluster,
			Key:     name,
		})

	case "storage.set":
		return pkgcore.StorageSet(ctx, pkgcore.StorageSetRequest{
			Cluster:    e.cluster,
			Storage:    e.storage,
			Name:       name,
			Bucket:     utils.GetString(params, "bucket"),
			CORS:       utils.GetBool(params, "cors"),
			ExpireDays: utils.GetInt(params, "expire_days"),
		})

	case "storage.delete":
		return pkgcore.StorageDelete(ctx, pkgcore.StorageDeleteRequest{
			Cluster: e.cluster,
			Storage: e.storage,
			Name:    name,
		})

	case "storage.empty":
		return pkgcore.StorageEmpty(ctx, pkgcore.StorageEmptyRequest{
			Cluster: e.cluster,
			Storage: e.storage,
			Name:    name,
		})

	case "service.set":
		return pkgcore.ServiceSet(ctx, pkgcore.ServiceSetRequest{
			Cluster:    e.cluster,
			Name:       name,
			Image:      utils.GetString(params, "image"),
			Port:       utils.GetInt(params, "port"),
			Command:    utils.GetString(params, "command"),
			Replicas:   utils.GetInt(params, "replicas"),
			EnvVars:    utils.GetStringSlice(params, "env"),
			Secrets:    utils.GetStringSlice(params, "secrets"),
			Storages:   utils.GetStringSlice(params, "storage"),
			Volumes:    utils.GetStringSlice(params, "volumes"),
			HealthPath: utils.GetString(params, "health"),
			Servers:    utils.GetStringSlice(params, "servers"),
		})

	case "service.delete":
		return pkgcore.ServiceDelete(ctx, pkgcore.ServiceDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case "cron.set":
		return pkgcore.CronSet(ctx, pkgcore.CronSetRequest{
			Cluster:  e.cluster,
			Name:     name,
			Image:    utils.GetString(params, "image"),
			Command:  utils.GetString(params, "command"),
			EnvVars:  utils.GetStringSlice(params, "env"),
			Secrets:  utils.GetStringSlice(params, "secrets"),
			Storages: utils.GetStringSlice(params, "storage"),
			Volumes:  utils.GetStringSlice(params, "volumes"),
			Schedule: utils.GetString(params, "schedule"),
			Servers:  utils.GetStringSlice(params, "servers"),
		})

	case "cron.run":
		return pkgcore.CronRun(ctx, pkgcore.CronRunRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case "cron.delete":
		return pkgcore.CronDelete(ctx, pkgcore.CronDeleteRequest{
			Cluster: e.cluster,
			Name:    name,
		})

	case "dns.set":
		return pkgcore.DNSSet(ctx, pkgcore.DNSSetRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	case "dns.delete":
		return pkgcore.DNSDelete(ctx, pkgcore.DNSDeleteRequest{
			Cluster: e.cluster,
			DNS:     e.dns,
			Service: name,
			Domains: utils.GetStringSlice(params, "domains"),
		})

	case "ingress.set":
		return pkgcore.IngressSet(ctx, pkgcore.IngressSetRequest{
			Cluster: e.cluster,
			Route: pkgcore.IngressRouteArg{
				Service: utils.GetString(params, "service"),
				Domains: utils.GetStringSlice(params, "domains"),
			},
			ACME: true,
		})

	case "ingress.delete":
		return pkgcore.IngressDelete(ctx, pkgcore.IngressDeleteRequest{
			Cluster: e.cluster,
			Route: pkgcore.IngressRouteArg{
				Service: utils.GetString(params, "service"),
				Domains: utils.GetStringSlice(params, "domains"),
			},
		})

	default:
		return fmt.Errorf("unknown command kind: %s", kind)
	}
}

// ── input validation ────────────────────────────────────────────────────────

// validateDispatchInput validates user input at the API boundary before dispatch.
// Same rules as config validation — ValidateName for resource names,
// ValidateEnvVarName for secret keys, ValidateDomain for domains.
func validateDispatchInput(kind, name string, params map[string]any) error {
	switch kind {
	case "secret.set", "secret.delete":
		if name != "" {
			return utils.ValidateEnvVarName("secret key", name)
		}
	case "dns.set", "dns.delete", "ingress.set", "ingress.delete":
		for _, d := range utils.GetStringSlice(params, "domains") {
			if err := utils.ValidateDomain("domain", d); err != nil {
				return err
			}
		}
		if name != "" {
			return utils.ValidateName("service", name)
		}
	case "firewall.set":
		// No name validation needed — firewall name is derived from app+env.
	default:
		// All other kinds: resource name must be DNS-1123 if provided.
		if name != "" {
			return utils.ValidateName("name", name)
		}
	}
	return nil
}

// ── firewall param parsing ──────────────────────────────────────────────────

func parseFirewallFromParams(params map[string]any) (provider.PortAllowList, error) {
	if params == nil {
		return nil, nil
	}
	preset := utils.GetString(params, "preset")
	rulesRaw, hasRules := params["rules"]

	var args []string
	if preset != "" {
		args = append(args, preset)
	}
	if hasRules {
		if rulesMap, ok := rulesRaw.(map[string]any); ok {
			for port, v := range rulesMap {
				if cidrs, ok := v.([]any); ok {
					for _, cidr := range cidrs {
						if s, ok := cidr.(string); ok {
							args = append(args, fmt.Sprintf("%s:%s", port, s))
						}
					}
				}
			}
		}
		if rulesMap, ok := rulesRaw.(map[string][]string); ok {
			for port, cidrs := range rulesMap {
				for _, cidr := range cidrs {
					args = append(args, fmt.Sprintf("%s:%s", port, cidr))
				}
			}
		}
	}

	return provider.ResolveFirewallArgs(context.Background(), args)
}

// ── JSONL output ────────────────────────────────────────────────────────────

// jsonlOutput implements pkg/core.Output, writing JSONL lines to a writer.
// Used by /run to stream output back to the cloud CLI.
type jsonlOutput struct {
	w io.Writer
}

var _ pkgcore.Output = (*jsonlOutput)(nil)

func (o *jsonlOutput) emit(ev pkgcore.Event) {
	line := pkgcore.MarshalEvent(ev)
	o.w.Write([]byte(line + "\n"))
}

func (o *jsonlOutput) Command(command, action, name string, extra ...any) {
	o.emit(pkgcore.NewCommandEvent(command, action, name, extra...))
}

func (o *jsonlOutput) Progress(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventProgress, msg))
}

func (o *jsonlOutput) Success(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventSuccess, msg))
}

func (o *jsonlOutput) Warning(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventWarning, msg))
}

func (o *jsonlOutput) Info(msg string) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventInfo, msg))
}

func (o *jsonlOutput) Error(err error) {
	o.emit(pkgcore.NewMessageEvent(pkgcore.EventError, err.Error()))
}

func (o *jsonlOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			o.emit(pkgcore.NewMessageEvent(pkgcore.EventStream, scanner.Text()))
		}
	}()
	return pw
}
