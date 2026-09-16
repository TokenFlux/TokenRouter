// 请求目标投影保留普通与 passthrough 对 Setup Token 的不同处理。
package service

import (
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) openAIRequestTarget(c *gin.Context, a *Account, passthrough bool) forward.RequestTargetOptions {
	oauth := a.Type == AccountTypeOAuth || a.Type == AccountTypeSetupToken && (!passthrough || a.IsOpenAIOAuthLike())
	return forward.RequestTargetOptions{
		OAuthTarget: oauth, APIKey: a.Type == AccountTypeAPIKey, DefaultURL: openaiPlatformAPIURL, CodexURL: chatgptCodexURL,
		BaseURL: func() string {
			base := a.GetOpenAIBaseURL()
			if _, unified := a.Credentials[upstreamProtocolsKey]; a.UsesNativeCNResponses() && (unified || a.IsAdaptiveAPIProtocol()) {
				base = a.GetCNProtocolBaseURL(APIProtocolResponses)
			}
			return base
		}, Validate: s.validateUpstreamBaseURL, FromBase: func(base string) string { return buildOpenAIResponsesURLForPlatform(a.Platform, base) }, AppendSuffix: func(base string) string {
			return appendOpenAIResponsesRequestPathSuffix(base, openAIResponsesRequestPathSuffix(c))
		},
	}
}
