package app

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// provideOpenAIQuota 直接组合账号查询、原连接写入及共享 task 协调器。
func provideOpenAIQuota(admin *account.Admin, store *postgres.AccountStore, proxies egress.ProxyRepository, transport httpclient.UpstreamTransport, token *account.OpenAITokenSource, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, gateway *service.OpenAIGatewayService) *account.OpenAIQuotaService {
	factory := &provider.OpenAIQuotaFactory{
		Proxy: proxies.GetByID, Transport: transport, Profiles: profiles, Routers: routers,
		Tasks: account.SharedOpenAITaskCoordinator(),
		TaskOptions: account.OpenAITaskOptions{
			Read: store.GetByID,
			Register: func(ctx context.Context, value *account.Record) (string, error) {
				return provider.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
			},
			Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
				_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
				return err
			},
			Invalidate: gateway.InvalidateAgentIdentityWSConnections,
		},
	}
	return account.NewOpenAIQuotaService(account.OpenAIQuotaOptions{
		Configured: func() bool { return admin != nil && transport != nil },
		Read:       admin.GetAccount, Token: token.GetAccessToken, Client: factory.Client,
		SaveExtra: store.UpdateExtra, RedeemID: openai.GenerateOpenAIQuotaRedeemRequestID,
		Warn: slog.Warn, Info: slog.Info,
	})
}
