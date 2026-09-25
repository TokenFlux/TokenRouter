package app

import (
	"context"
	"log"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// provideManagedRefresh 绑定原生账号用例与唯一刷新协调器，构造不执行交换。
func provideManagedRefresh(admin *account.Admin, privacy *account.PrivacyService, coordinator *account.OAuthRefreshAPI, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, claude *account.ClaudeAuthorization, openai *account.OpenAIAuthorization, gemini *account.GeminiAuthorization, ag *account.AntigravityAuthorization, grok account.GrokRefreshTokenService, invalidator account.TokenCacheInvalidator) *account.ManagedRefreshService {
	qoder := accountprovider.NewQoderTokenRefresher(accountprovider.QoderRefreshOptions{Transport: transport, Profiles: profiles})
	source := &account.ManualCredentialExchange{Claude: claude, OpenAI: openai, Gemini: gemini, Antigravity: ag, Grok: grok, Qoder: qoder.Refresh}
	exchange := func(ctx context.Context, value *account.Record) (account.ManagedRefreshObservation, error) {
		credentials, missing, err := source.Refresh(ctx, value)
		return account.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
	}
	var invalidate func(context.Context, *account.Record) error
	if invalidator != nil {
		invalidate = func(ctx context.Context, value *account.Record) error {
			return invalidator.InvalidateToken(ctx, value)
		}
	}
	return account.NewManagedRefreshService(account.ManagedRefreshOptions{Store: admin, Privacy: privacy, Coordinate: coordinator.WithManagedRefresh, CacheKey: accountprovider.ManagedRefreshCacheKey, Exchange: exchange, Invalidate: invalidate, Log: log.Printf, Warn: slog.Warn, Error: slog.Error})
}
