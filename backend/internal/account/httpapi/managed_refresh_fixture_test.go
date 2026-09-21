package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// newManagedRefreshFixture 保留原独立构造的单次协调，只注入当前测试的平台交换。
func newManagedRefreshFixture(source interface {
	account.ManagedCredentialStore
	account.ManagedCredentialPrivacy
}, exchange *account.ManualCredentialExchange) *account.ManagedRefreshService {
	return account.NewManagedRefreshService(account.ManagedRefreshOptions{Store: source, Privacy: source, CacheKey: provider.ManagedRefreshCacheKey,
		Coordinate: func(ctx context.Context, v *account.Record, _ string, apply func(context.Context, *account.Record) (*account.Record, string, error)) (*account.Record, string, error) {
			return apply(ctx, v)
		},
		Exchange: func(ctx context.Context, v *account.Record) (account.ManagedRefreshObservation, error) {
			credentials, missing, err := exchange.Refresh(ctx, v)
			return account.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
		}})
}
