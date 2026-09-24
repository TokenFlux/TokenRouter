package httpapi

import (
	"context"
	"net/http"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// @project-doc docs/interfaces/grok_upstream.md#grok_account_contract
// GrokExecutor 绑定单次平台执行的固定依赖，账号切换和完成处理由请求拥有者负责。
type GrokExecutor struct {
	FastPolicy  *provider.ExecutionFastPolicy
	Credentials *provider.RequestCredentials
	Transport   httpclient.UpstreamTransport
	Failure     *UpstreamTransportFailure
	Output      *OpenAIResponseOutput
	Health      *accountprovider.GrokHealth
	Routes      provider.GrokRoutes
	TLS         *egressprovider.TLSProfiles
	Dialer      openai.WSClientDialer
	Enter       func() (func(), error)
}

func (s *GrokExecutor) TLSProfile(target *provider.ExecutionAccount, matches ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.TLS == nil {
		return nil
	}
	return s.TLS.ResolveRequestTLS(provider.ExecutionTLSSelection(target, matches))
}

// BuildResponsesRequest 在原调用位置选择动态默认地址，并只转发允许的请求头。
func (s *GrokExecutor) BuildResponsesRequest(ctx context.Context, c *gin.Context, target *provider.ExecutionAccount, body []byte, token, identity string, runtimeDefault bool) (*http.Request, error) {
	targetURL, err := s.Routes.Responses(target, runtimeDefault)
	if err != nil {
		return nil, err
	}
	beta := ""
	if c != nil {
		beta = c.GetHeader("OpenAI-Beta")
	}
	return grok.BuildResponsesRequest(ctx, body, grok.ResponsesRequestOptions{
		URL: targetURL, Token: token, CacheIdentity: identity, OAuth: target.View().IsGrokOAuth(), OpenAIBeta: beta,
		Profile: func(ctx context.Context) context.Context {
			return upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileGrok)
		},
		ApplyOverrides: provider.BindExecutionHeaders(target),
	})
}
