package admin

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log"
	"log/slog"
)

// 独立旧构造只投影现有端口；生产 provider 注入带共享协调器的新用例。
type legacyManagedAdmin struct{ source service.AdminService }

func (a legacyManagedAdmin) GetAccount(ctx context.Context, id int64) (*account.Record, error) {
	v, err := a.source.GetAccount(ctx, id)
	return service.AccountRecordView(v), err
}
func (a legacyManagedAdmin) UpdateAccount(ctx context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	v, err := a.source.UpdateAccount(ctx, id, input)
	return service.AccountRecordView(v), err
}
func (a legacyManagedAdmin) ClearAccountError(ctx context.Context, id int64) (*account.Record, error) {
	v, err := a.source.ClearAccountError(ctx, id)
	return service.AccountRecordView(v), err
}

// 旧独立构造也必须提供条件恢复，不能退回无条件清错。
func (a legacyManagedAdmin) ClearManagedRefreshError(ctx context.Context, value *account.Record) (*account.Record, bool, error) {
	recovery, ok := a.source.(interface {
		ClearManagedRefreshError(context.Context, *service.Account) (*service.Account, bool, error)
	})
	if !ok {
		return nil, false, account.ErrManagedRecoveryUnavailable
	}
	v, applied, err := recovery.ClearManagedRefreshError(ctx, service.AccountFromRecord(value))
	return service.AccountRecordView(v), applied, err
}
func (a legacyManagedAdmin) EnsureOpenAIPrivacy(ctx context.Context, v *account.Record) string {
	return a.source.EnsureOpenAIPrivacy(ctx, service.AccountFromRecord(v))
}
func (a legacyManagedAdmin) EnsureAntigravityPrivacy(ctx context.Context, v *account.Record) string {
	return a.source.EnsureAntigravityPrivacy(ctx, service.AccountFromRecord(v))
}
func (h *AccountHandler) legacyManagedRefresh(original *service.Account) *account.ManagedRefreshService {
	adapter := service.NewManualCredentialExchange(service.ManualCredentialExchangeOptions{Admin: h.adminService, Claude: h.oauthService, OpenAI: h.openaiOAuthService, Gemini: h.geminiOAuthService, Antigravity: h.antigravityOAuthService, Grok: h.grokOAuthService, Qoder: func(ctx context.Context, v *service.Account) (map[string]any, error) {
		return newQoderTokenRefresherForAdmin(h.adminService, nil).Refresh(ctx, v)
	}})
	store := legacyManagedAdmin{h.adminService}
	options := account.ManagedRefreshOptions{Store: store, Privacy: store, Log: log.Printf, Warn: slog.Warn, Error: slog.Error, CacheKey: func(v *account.Record) string { return service.ManagedRefreshCacheKey(service.AccountFromRecord(v)) },
		Coordinate: func(ctx context.Context, v *account.Record, _ string, apply func(context.Context, *account.Record) (*account.Record, string, error)) (*account.Record, string, error) {
			return apply(ctx, v)
		},
		Exchange: func(ctx context.Context, value *account.Record) (account.ManagedRefreshObservation, error) {
			current := original
			if current == nil {
				current = service.AccountFromRecord(value)
			}
			credentials, missing, err := adapter.Refresh(ctx, current)
			return account.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
		},
	}
	if h.tokenCacheInvalidator != nil {
		options.Invalidate = func(ctx context.Context, v *account.Record) error {
			return h.tokenCacheInvalidator.InvalidateToken(ctx, service.AccountFromRecord(v))
		}
	}
	return account.NewManagedRefreshService(options)
}

func (a legacyManagedAdmin) UpdateAccountExtra(ctx context.Context, id int64, updates map[string]any) error {
	return a.source.UpdateAccountExtra(ctx, id, updates)
}
