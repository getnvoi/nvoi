package handlers

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"github.com/getnvoi/nvoi/pkg/provider"
	"gorm.io/gorm"
)

type ListDNSInput struct{ RepoScopedInput }
type ListDNSOutput struct{ Body []provider.DNSRecord }

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
				Creds: resolveRepoCreds(ctx, repo, "dns", repo.DNSProvider),
			},
		})
		if err != nil {
			return nil, humaError(err)
		}

		return &ListDNSOutput{Body: records}, nil
	}
}
