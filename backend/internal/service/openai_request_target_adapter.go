// 请求目标投影保留普通与 passthrough 对 Setup Token 的不同处理。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) openAIRequestTarget(c *gin.Context, a *gatewayprovider.ExecutionAccount, passthrough bool) forward.RequestTargetOptions {
	oauth := a.Record.Type == capability.AccountTypeOAuth || a.Record.Type == capability.AccountTypeSetupToken && (!passthrough || a.View().IsOpenAIOAuthLike())
	return forward.RequestTargetOptions{
		OAuthTarget: oauth, APIKey: a.Record.Type == capability.AccountTypeAPIKey, DefaultURL: openaiPlatformAPIURL, CodexURL: chatgptCodexURL,
		BaseURL: func() string {
			base := gatewayprovider.ExecutionProtocolTarget(a).GetOpenAIBaseURL()
			if _, unified := a.Record.Credentials[account.UpstreamProtocolsKey]; gatewayprovider.ExecutionProtocolTarget(a).UsesNativeCNResponses() && (unified || gatewayprovider.ExecutionProtocolTarget(a).IsAdaptiveAPIProtocol()) {
				base = gatewayprovider.ExecutionProtocolTarget(a).GetCNProtocolBaseURL(account.APIProtocolResponses)
			}
			return base
		}, Validate: s.validateUpstreamBaseURL, FromBase: func(base string) string { return forward.ResponsesEndpoint(a.Record.Platform, base) }, AppendSuffix: func(base string) string {
			return appendOpenAIResponsesRequestPathSuffix(base, httpapi.OpenAIResponsesRequestPathSuffix(c))
		},
	}
}
