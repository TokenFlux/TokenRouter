package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// grok_executor.go binds Grok execution dependencies and builds authenticated
// Responses requests for the selected provider.
// Grok Responses request construction uses the selected provider's route and
// credentials, with CLI headers reserved for OAuth providers. Header overrides
// run through the shared execution binding, and the supplied cache identity
// ties body preparation to the upstream conversation header.

// @project-doc docs/interfaces/grok_upstream.md#grok_account_contract
// GrokExecutor binds single-attempt platform dependencies; the request owner handles failover and completion.
type GrokExecutor struct {
	Attributes  *routing.ModelAttributeService      // resolved model capability service
	FastPolicy  *gatewayadapter.ExecutionFastPolicy // request service-tier policy
	Credentials *gatewayadapter.RequestCredentials  // shared request credential resolver
	Transport   httpclient.UpstreamTransport        // provider-isolated HTTP transport
	Failure     *UpstreamTransportFailure           // transport failure presentation
	Output      *OpenAIResponseOutput               // shared Responses output handling
	Health      *provideradapter.GrokHealth         // Grok provider health observer
	Routes      gatewayadapter.GrokRoutes           // provider endpoint route resolver
	// TLS selects a request fingerprint; a nil service lets TLSProfile return nil
	// for the shared transport's ordinary profile.
	TLS    *egressprovider.TLSProfiles
	Dialer openai.WSClientDialer // shared upstream WebSocket dialer
	// Enter registers the platform attempt with application shutdown tracking.
	// A successful entry supplies cleanup that the invoked executor releases.
	Enter func() (func(), error)
}

// TLSProfile resolves this provider's TLS selection with optional router
// matches. A nil executor or missing TLS service returns nil, allowing the
// shared HTTP transport to use its ordinary profile.
func (s *GrokExecutor) TLSProfile(target *gatewayadapter.ExecutionProvider, matches ...egress.TLSFingerprintRouterMatchResult) *tlsfingerprint.Profile {
	if s == nil || s.TLS == nil {
		return nil
	}
	return s.TLS.ResolveRequestTLS(gatewayadapter.ExecutionTLSSelection(target, matches))
}

// BuildResponsesRequest builds one authenticated Responses request using the
// selected provider route and optional runtime default. It forwards
// OpenAI-Beta and supplies OAuth CLI and cache headers through Grok construction
// before applying supported provider overrides.
func (s *GrokExecutor) BuildResponsesRequest(ctx context.Context, c *gin.Context, target *gatewayadapter.ExecutionProvider, body []byte, token, identity string, runtimeDefault bool) (*http.Request, error) {
	targetURL, err := s.Routes.Responses(target, runtimeDefault)
	if err != nil {
		return nil, err
	}
	body = stripOpenAIWireEffortIfMatched(ctx, c, target, body, protocol.ProtocolOpenAIResponses)
	beta := ""
	if c != nil {
		beta = c.GetHeader("OpenAI-Beta")
	}
	return grok.BuildResponsesRequest(ctx, body, grok.ResponsesRequestOptions{
		URL: targetURL, Token: token, CacheIdentity: identity, OAuth: target.View().IsGrokOAuth(), OpenAIBeta: beta,
		Profile: func(ctx context.Context) context.Context {
			return upstream.WithHTTPUpstreamProfile(ctx, upstream.HTTPUpstreamProfileGrok)
		},
		ApplyOverrides: gatewayadapter.BindExecutionHeaders(target),
	})
}
