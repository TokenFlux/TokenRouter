package app

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/google/uuid"
)

// provideCodexInvites 直接装配账号用例及平台端口，复用请求侧令牌与传输实例。
func provideCodexInvites(admin *account.Admin, proxy egress.ProxyRepository, token *account.OpenAITokenSource, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, routers provider.OpenAITokenRouterReader) *account.CodexInviteResetService {
	factory := provider.CodexInviteFactory{Token: token, Proxy: proxy.GetByID, Transport: transport, Profiles: profiles, Routers: routers}
	return &account.CodexInviteResetService{Options: account.CodexInviteResetOptions{
		Read: admin.GetAccount, Client: factory.Client, NewID: uuid.NewString, Warn: slog.Warn,
	}}
}
