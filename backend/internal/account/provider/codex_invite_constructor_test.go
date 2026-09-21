package provider

import (
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/google/uuid"
)

// 测试将原固定应答接入实际账号用例与平台客户端，保留完整请求断言。
func newCodexInviteForTest(reader codexInviteResetAdminServiceStub, transport QoderTransport, token *account.OpenAITokenSource, profiles *egressprovider.TLSProfiles, routers OpenAITokenRouterReader) *account.CodexInviteResetService {
	factory := CodexInviteFactory{Token: token, Proxy: reader.GetProxy, Transport: transport, Profiles: profiles, Routers: routers}
	return &account.CodexInviteResetService{Options: account.CodexInviteResetOptions{
		Read: reader.GetAccount, Client: factory.Client, NewID: uuid.NewString, Warn: slog.Warn,
	}}
}
