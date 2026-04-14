package handlers

import (
	"context"

	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

type ListSecretsInput struct{ RepoScopedInput }
type ListSecretsOutput struct{ Body []string }

func ListSecrets(db *gorm.DB) func(context.Context, *ListSecretsInput) (*ListSecretsOutput, error) {
	return func(ctx context.Context, input *ListSecretsInput) (*ListSecretsOutput, error) {
		cluster, err := repoCluster(ctx, db, input.RepoScopedInput)
		if err != nil {
			return nil, err
		}
		keys, err := pkgcore.SecretList(ctx, pkgcore.SecretListRequest{Cluster: *cluster})
		if err != nil {
			return nil, humaError(err)
		}
		return &ListSecretsOutput{Body: keys}, nil
	}
}
