package handlers

import (
	"context"

	"github.com/getnvoi/nvoi/internal/api"
	"github.com/getnvoi/nvoi/internal/config"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type ListSecretsInput struct{ RepoScopedInput }
type ListSecretsOutput struct{ Body []string }

func ListSecrets(db *gorm.DB) func(context.Context, *ListSecretsInput) (*ListSecretsOutput, error) {
	return func(ctx context.Context, input *ListSecretsInput) (*ListSecretsOutput, error) {
		user := api.UserFromContext(ctx)
		repo, err := findRepo(db, user.ID, input.WorkspaceID, input.RepoID)
		if err != nil {
			return nil, err
		}
		var secretNames []string
		if repo.Config != "" {
			if cfg, err := config.ParseAppConfig([]byte(repo.Config)); err == nil {
				secretNames = cfg.Secrets
			}
		}
		keys, err := pkgcore.SecretList(ctx, pkgcore.SecretListRequest{SecretNames: secretNames})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListSecretsOutput{Body: keys}, nil
	}
}
