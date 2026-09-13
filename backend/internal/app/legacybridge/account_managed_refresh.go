package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// ManagedRefreshExchange 只投影具体供应商适配，S09 改绑；不执行账号管理或维护规则。
func ManagedRefreshExchange(options service.ManualCredentialExchangeOptions) func(context.Context, *account.Record) (account.ManagedRefreshObservation, error) {
	source := service.NewManualCredentialExchange(options)
	return func(ctx context.Context, v *account.Record) (account.ManagedRefreshObservation, error) {
		credentials, missing, err := source.Refresh(ctx, service.AccountFromRecord(v))
		return account.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
	}
}
func ManagedRefreshCacheKey(v *account.Record) string {
	return service.ManagedRefreshCacheKey(service.AccountFromRecord(v))
}
func ManagedRefreshInvalidation(source service.TokenCacheInvalidator) func(context.Context, *account.Record) error {
	if source == nil {
		return nil
	}
	return func(ctx context.Context, v *account.Record) error {
		return source.InvalidateToken(ctx, service.AccountFromRecord(v))
	}
}
