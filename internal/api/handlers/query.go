package handlers

import (
	"context"
	"io"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"gorm.io/gorm"
)

// ── Input / Output types ─────────────────────────────────────────────────────

type ListInstancesInput struct{ RepoScopedInput }
type ListInstancesOutput struct{ Body []*provider.Server }

type ListVolumesInput struct{ RepoScopedInput }
type ListVolumesOutput struct{ Body []*provider.Volume }

type ListDNSInput struct{ RepoScopedInput }
type ListDNSOutput struct{ Body []provider.DNSRecord }

type ListSecretsInput struct{ RepoScopedInput }
type ListSecretsOutput struct{ Body []string }

type ListStorageInput struct{ RepoScopedInput }
type ListStorageOutput struct{ Body []pkgcore.StorageItem }

type EmptyStorageInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Storage name"`
}
type EmptyStorageOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type ListBuildsInput struct{ RepoScopedInput }
type ListBuildsOutput struct{ Body []pkgcore.RegistryImage }

type BuildLatestInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Build name"`
}
type BuildLatestOutput struct {
	Body struct {
		Image string `json:"image"`
	}
}

type BuildPruneInput struct {
	RepoScopedInput
	Name string `path:"name" doc:"Build name"`
	Body struct {
		Keep int `json:"keep" required:"true" minimum:"1" doc:"Number of tags to keep"`
	}
}
type BuildPruneOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

type ServiceLogsInput struct {
	RepoScopedInput
	Service    string `path:"service" doc:"Service name"`
	Follow     string `query:"follow" default:"false" doc:"Follow log output"`
	Tail       string `query:"tail" default:"50" doc:"Lines from end"`
	Since      string `query:"since" doc:"Show logs since duration (e.g. 5m)"`
	Previous   string `query:"previous" default:"false" doc:"Previous container logs"`
	Timestamps string `query:"timestamps" default:"false" doc:"Include timestamps"`
}

type ExecInput struct {
	RepoScopedInput
	Service string `path:"service" doc:"Service name"`
	Body    struct {
		Command []string `json:"command" required:"true" doc:"Command to run"`
	}
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func ListInstances(db *gorm.DB) func(context.Context, *ListInstancesInput) (*ListInstancesOutput, error) {
	return func(ctx context.Context, input *ListInstancesInput) (*ListInstancesOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		servers, err := pkgcore.ComputeList(ctx, pkgcore.ComputeListRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &ListInstancesOutput{Body: servers}, nil
	}
}

func ListVolumes(db *gorm.DB) func(context.Context, *ListVolumesInput) (*ListVolumesOutput, error) {
	return func(ctx context.Context, input *ListVolumesInput) (*ListVolumesOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		volumes, err := pkgcore.VolumeList(ctx, pkgcore.VolumeListRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &ListVolumesOutput{Body: volumes}, nil
	}
}

func ListDNSRecords(db *gorm.DB) func(context.Context, *ListDNSInput) (*ListDNSOutput, error) {
	return func(ctx context.Context, input *ListDNSInput) (*ListDNSOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		if repo.DNSProvider == nil {
			return nil, huma.Error400BadRequest("no dns provider configured — run 'nvoi provider set dns <name>' first")
		}

		records, err := pkgcore.DNSList(ctx, pkgcore.DNSListRequest{
			DNS: pkgcore.ProviderRef{
				Name:  repo.DNSProvider.Provider,
				Creds: repo.DNSProvider.CredentialsMap(),
			},
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &ListDNSOutput{Body: records}, nil
	}
}

func ListSecrets(db *gorm.DB) func(context.Context, *ListSecretsInput) (*ListSecretsOutput, error) {
	return func(ctx context.Context, input *ListSecretsInput) (*ListSecretsOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		keys, err := pkgcore.SecretList(ctx, pkgcore.SecretListRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &ListSecretsOutput{Body: keys}, nil
	}
}

func ListStorageBuckets(db *gorm.DB) func(context.Context, *ListStorageInput) (*ListStorageOutput, error) {
	return func(ctx context.Context, input *ListStorageInput) (*ListStorageOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		items, err := pkgcore.StorageList(ctx, pkgcore.StorageListRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &ListStorageOutput{Body: items}, nil
	}
}

func EmptyStorage(db *gorm.DB) func(context.Context, *EmptyStorageInput) (*EmptyStorageOutput, error) {
	return func(ctx context.Context, input *EmptyStorageInput) (*EmptyStorageOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}

		if repo.StorageProvider == nil {
			return nil, huma.Error400BadRequest("no storage provider configured")
		}

		cluster := clusterFromRepo(repo)
		err = pkgcore.StorageEmpty(ctx, pkgcore.StorageEmptyRequest{
			Cluster: *cluster,
			Storage: pkgcore.ProviderRef{
				Name:  repo.StorageProvider.Provider,
				Creds: repo.StorageProvider.CredentialsMap(),
			},
			Name: input.Name,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}

		return &EmptyStorageOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "emptied"}}, nil
	}
}

func ListBuilds(db *gorm.DB) func(context.Context, *ListBuildsInput) (*ListBuildsOutput, error) {
	return func(ctx context.Context, input *ListBuildsInput) (*ListBuildsOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		images, err := pkgcore.BuildList(ctx, pkgcore.BuildListRequest{Cluster: *cluster})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &ListBuildsOutput{Body: images}, nil
	}
}

func BuildLatestImage(db *gorm.DB) func(context.Context, *BuildLatestInput) (*BuildLatestOutput, error) {
	return func(ctx context.Context, input *BuildLatestInput) (*BuildLatestOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		ref, err := pkgcore.BuildLatest(ctx, pkgcore.BuildLatestRequest{
			Cluster: *cluster,
			Name:    input.Name,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &BuildLatestOutput{Body: struct {
			Image string `json:"image"`
		}{Image: ref}}, nil
	}
}

func PruneBuild(db *gorm.DB) func(context.Context, *BuildPruneInput) (*BuildPruneOutput, error) {
	return func(ctx context.Context, input *BuildPruneInput) (*BuildPruneOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		err = pkgcore.BuildPrune(ctx, pkgcore.BuildPruneRequest{
			Cluster: *cluster,
			Name:    input.Name,
			Keep:    input.Body.Keep,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &BuildPruneOutput{Body: struct {
			Status string `json:"status"`
		}{Status: "pruned"}}, nil
	}
}

// ── Database ────────────────────────────────────────────────────────────────

type DatabaseBackupListInput struct {
	RepoScopedInput
	Name string `query:"name" default:"main" doc:"Database name"`
}
type DatabaseBackupListOutput struct{ Body []pkgcore.BackupEntry }

type DatabaseBackupDownloadInput struct {
	RepoScopedInput
	Name string `query:"name" default:"main" doc:"Database name"`
	Key  string `path:"key" doc:"Backup key"`
}

type DatabaseSQLInput struct {
	RepoScopedInput
	Body struct {
		Name  string `json:"name,omitempty" doc:"Database name (defaults to main)"`
		Query string `json:"query" required:"true" doc:"SQL query"`
	}
}
type DatabaseSQLOutput struct {
	Body struct {
		Output string `json:"output"`
	}
}

func DatabaseBackupList(db *gorm.DB) func(context.Context, *DatabaseBackupListInput) (*DatabaseBackupListOutput, error) {
	return func(ctx context.Context, input *DatabaseBackupListInput) (*DatabaseBackupListOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		entries, err := pkgcore.DatabaseBackupList(ctx, pkgcore.DatabaseBackupListRequest{
			Cluster: *cluster,
			DBName:  input.Name,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &DatabaseBackupListOutput{Body: entries}, nil
	}
}

func DatabaseBackupDownload(db *gorm.DB) func(context.Context, *DatabaseBackupDownloadInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *DatabaseBackupDownloadInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		body, contentLength, err := pkgcore.DatabaseBackupDownload(ctx, pkgcore.DatabaseBackupDownloadRequest{
			Cluster: *cluster,
			DBName:  input.Name,
			Key:     input.Key,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "application/octet-stream")
				if contentLength > 0 {
					ctx.SetHeader("Content-Length", strconv.FormatInt(contentLength, 10))
				}
				defer body.Close()
				io.Copy(ctx.BodyWriter(), body)
			},
		}, nil
	}
}

func DatabaseSQL(db *gorm.DB) func(context.Context, *DatabaseSQLInput) (*DatabaseSQLOutput, error) {
	return func(ctx context.Context, input *DatabaseSQLInput) (*DatabaseSQLOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		name := input.Body.Name
		if name == "" {
			name = "main"
		}
		out, err := pkgcore.DatabaseSQL(ctx, pkgcore.DatabaseSQLRequest{
			Cluster: *cluster,
			DBName:  name,
			Query:   input.Body.Query,
		})
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		return &DatabaseSQLOutput{Body: struct {
			Output string `json:"output"`
		}{Output: out}}, nil
	}
}

func ServiceLogs(db *gorm.DB) func(context.Context, *ServiceLogsInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *ServiceLogsInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		tail, _ := strconv.Atoi(input.Tail)
		if tail == 0 {
			tail = 50
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/plain")
				cluster.Output = &streamOutput{w: ctx.BodyWriter()}

				logsErr := pkgcore.Logs(ctx.Context(), pkgcore.LogsRequest{
					Cluster:    *cluster,
					Service:    input.Service,
					Follow:     input.Follow == "true",
					Tail:       tail,
					Since:      input.Since,
					Previous:   input.Previous == "true",
					Timestamps: input.Timestamps == "true",
				})
				if logsErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + logsErr.Error() + "\n"))
				}
			},
		}, nil
	}
}

func ExecCommand(db *gorm.DB) func(context.Context, *ExecInput) (*huma.StreamResponse, error) {
	return func(ctx context.Context, input *ExecInput) (*huma.StreamResponse, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}

		return &huma.StreamResponse{
			Body: func(ctx huma.Context) {
				ctx.SetHeader("Content-Type", "text/plain")
				cluster.Output = &streamOutput{w: ctx.BodyWriter()}

				execErr := pkgcore.Exec(ctx.Context(), pkgcore.ExecRequest{
					Cluster: *cluster,
					Service: input.Service,
					Command: input.Body.Command,
				})
				if execErr != nil {
					ctx.BodyWriter().Write([]byte("\nerror: " + execErr.Error() + "\n"))
				}
			},
		}, nil
	}
}
