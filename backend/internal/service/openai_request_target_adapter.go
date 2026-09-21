// 请求目标投影保留普通与 passthrough 对 Setup Token 的不同处理。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) openAIRequestTarget(c *gin.Context, a *Account, passthrough bool) forward.RequestTargetOptions {
	oauth := a.Type == capability.AccountTypeOAuth || a.Type == capability.AccountTypeSetupToken && (!passthrough || a.IsOpenAIOAuthLike())
	return forward.RequestTargetOptions{
		OAuthTarget: oauth, APIKey: a.Type == capability.AccountTypeAPIKey, DefaultURL: openaiPlatformAPIURL, CodexURL: chatgptCodexURL,
		BaseURL: func() string {
			base := a.GetOpenAIBaseURL()
			if _, unified := a.Credentials[account.UpstreamProtocolsKey]; a.UsesNativeCNResponses() && (unified || a.IsAdaptiveAPIProtocol()) {
				base = a.GetCNProtocolBaseURL(account.APIProtocolResponses)
			}
			return base
		}, Validate: s.validateUpstreamBaseURL, FromBase: func(base string) string { return forward.ResponsesEndpoint(a.Platform, base) }, AppendSuffix: func(base string) string {
			return appendOpenAIResponsesRequestPathSuffix(base, httpapi.OpenAIResponsesRequestPathSuffix(c))
		},
	}
}
